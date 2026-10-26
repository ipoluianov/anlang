package types

import (
	"go/constant"
	"strings"
	"unicode/utf8"
)

// checkPrintf checks the arguments of fmt.Printf and fmt.Sprintf against
// a constant format string at compile time (ct.reject.precondition): the
// precondition fmt.Valid(format, meta.Types(args)) of these functions.
// A format string that is not constant cannot be checked and so does not
// prove the precondition.
func (c *checker) checkPrintf(name string, args []*operand) {
	if len(args) == 0 {
		return
	}
	f := args[0]
	if f.mode == invalid {
		return
	}
	if !isString(f.typ) {
		c.errorf(f.expr.Pos(), "expr.go", "cannot use %s as the format string of %s", f, name)
		return
	}
	if f.mode != constant_ {
		c.errorf(f.expr.Pos(), "ct.reject.precondition", "the format string of %s is not constant, so its precondition fmt.Valid cannot be proven", name)
		return
	}
	format := constant.StringVal(f.val)
	rest := args[1:]
	argNum := 0
	errorf := func(format string, a ...any) {
		c.errorf(f.expr.Pos(), "ct.reject.precondition", "%s: "+format, append([]any{name}, a...)...)
	}
	next := func() *operand {
		if argNum >= len(rest) {
			return nil
		}
		argNum++
		return rest[argNum-1]
	}
	for i := 0; i < len(format); {
		if format[i] != '%' {
			i++
			continue
		}
		start := i
		i++
		// Flags.
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			i++
		}
		if i < len(format) && format[i] == '[' {
			errorf("explicit argument indexes in %q are not supported", format[start:])
			return
		}
		// Width and precision.
		for _, part := range []bool{false, true} {
			if part {
				if i >= len(format) || format[i] != '.' {
					break
				}
				i++
			}
			if i < len(format) && format[i] == '*' {
				i++
				a := next()
				if a == nil {
					errorf("format %s reads a width or precision argument, but there are not enough arguments", format[start:i])
					return
				}
				if a.mode != invalid && !isInteger(a.typ) {
					errorf("the width or precision argument %s of %s must be an integer", a, format[start:i])
				}
				continue
			}
			for i < len(format) && '0' <= format[i] && format[i] <= '9' {
				i++
			}
		}
		if i >= len(format) {
			errorf("format %q ends without a verb", format[start:])
			return
		}
		verb, size := utf8.DecodeRuneInString(format[i:])
		i += size
		flags := format[start+1 : i-size]
		if verb == '%' {
			continue
		}
		a := next()
		if a == nil {
			errorf("format %s has no argument", format[start:i])
			return
		}
		if a.mode == invalid {
			continue
		}
		if strings.Contains(flags, "#") && verb == 'v' {
			errorf("format %%#v is not supported yet by this compiler (spec/24-roadmap.md, step 29)")
			continue
		}
		if !c.printfVerbOK(verb, a.typ, map[Type]bool{}) {
			errorf("format %s has argument %s of wrong type %s", format[start:i], exprString(a.expr), a.typ)
		}
	}
	if argNum < len(rest) {
		errorf("call has %d arguments, but the format %q reads %d", len(rest), format, argNum)
	}
}

// printfVerbOK reports whether verb applies to values of type t, as Go's
// vet decides.
func (c *checker) printfVerbOK(verb rune, t Type, seen map[Type]bool) bool {
	switch verb {
	case 'v', 'T':
		return true
	}
	if seen[t] {
		return true
	}
	seen[t] = true
	if isTypeParam(t) || isInterfaceType(t) {
		return true // the dynamic type is checked when the program runs
	}
	if strings.ContainsRune("sqxX", verb) && c.hasStringMethod(t) {
		return true
	}
	switch u := t.Underlying().(type) {
	case *Basic:
		switch {
		case u.info&IsBoolean != 0:
			return verb == 't'
		case u.info&IsInteger != 0:
			return strings.ContainsRune("bcdoOqxXU", verb)
		case u.info&IsFloat != 0:
			return strings.ContainsRune("beEfFgGxX", verb)
		case u.info&(IsString|IsTerminated) != 0:
			return strings.ContainsRune("sqxX", verb)
		}
	case *Slice:
		if b, ok := u.Elem.Underlying().(*Basic); ok && b.kind == U8 && strings.ContainsRune("sqxX", verb) {
			return true
		}
		return c.printfVerbOK(verb, u.Elem, seen)
	case *Array:
		return c.printfVerbOK(verb, u.Elem, seen)
	case *Map:
		return c.printfVerbOK(verb, u.Key, seen) && c.printfVerbOK(verb, u.Elem, seen)
	case *Struct:
		for _, f := range u.Fields {
			if !c.printfVerbOK(verb, f.typ, seen) {
				return false
			}
		}
		return true
	}
	return false
}

// hasStringMethod reports whether t has a method Error() string or
// String() string, which formats the value for %s and %v.
func (c *checker) hasStringMethod(t Type) bool {
	for _, name := range []string{"Error", "String"} {
		obj, _, _ := c.lookupFieldOrMethod(t, c.pkg, name)
		if f, ok := obj.(*Func); ok {
			sig := f.Signature()
			if sig.Params.Len() == 0 && sig.Results.Len() == 1 && isString(sig.Results.At(0).typ) {
				return true
			}
		}
	}
	return false
}
