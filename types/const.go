package types

import (
	"go/constant"
	"go/token"
	"strconv"

	"an/syntax"
)

// Operators of go/constant.
const (
	tokenAdd    = token.ADD
	tokenSub    = token.SUB
	tokenShl    = token.SHL
	tokenGeq    = token.GEQ
	tokenLeq    = token.LEQ
	tokenLss    = token.LSS
	tokenNeq    = token.NEQ
	tokenQuoInt = token.QUO_ASSIGN // integer division in constant.BinaryOp
	tokenRem    = token.REM
)

// constOp maps an operator of the syntax tree to the operator of
// go/constant.
var constOp = map[syntax.Kind]token.Token{
	syntax.Add:    token.ADD,
	syntax.Sub:    token.SUB,
	syntax.Mul:    token.MUL,
	syntax.Quo:    token.QUO,
	syntax.Rem:    token.REM,
	syntax.And:    token.AND,
	syntax.Or:     token.OR,
	syntax.Xor:    token.XOR,
	syntax.AndNot: token.AND_NOT,
	syntax.Shl:    token.SHL,
	syntax.Shr:    token.SHR,
	syntax.LAnd:   token.LAND,
	syntax.LOr:    token.LOR,
	syntax.Not:    token.NOT,
	syntax.Eql:    token.EQL,
	syntax.Neq:    token.NEQ,
	syntax.Lss:    token.LSS,
	syntax.Leq:    token.LEQ,
	syntax.Gtr:    token.GTR,
	syntax.Geq:    token.GEQ,
}

// assignOps maps an op-assign token to its binary operator.
var assignOps = map[syntax.Kind]syntax.Kind{
	syntax.AddAssign:    syntax.Add,
	syntax.SubAssign:    syntax.Sub,
	syntax.MulAssign:    syntax.Mul,
	syntax.QuoAssign:    syntax.Quo,
	syntax.RemAssign:    syntax.Rem,
	syntax.AndAssign:    syntax.And,
	syntax.OrAssign:     syntax.Or,
	syntax.XorAssign:    syntax.Xor,
	syntax.ShlAssign:    syntax.Shl,
	syntax.ShrAssign:    syntax.Shr,
	syntax.AndNotAssign: syntax.AndNot,
}

func isComparison(op syntax.Kind) bool {
	switch op {
	case syntax.Eql, syntax.Neq, syntax.Lss, syntax.Leq, syntax.Gtr, syntax.Geq:
		return true
	}
	return false
}

func isShift(op syntax.Kind) bool { return op == syntax.Shl || op == syntax.Shr }

// literalValue returns the constant value and untyped type of a basic
// literal (lex.literal.go).
func literalValue(lit *syntax.BasicLit) (constant.Value, Type, bool) {
	switch lit.Kind {
	case syntax.Int:
		v := constant.MakeFromLiteral(lit.Value, token.INT, 0)
		return v, Typ[UntypedInt], v.Kind() == constant.Int
	case syntax.Float:
		v := constant.MakeFromLiteral(lit.Value, token.FLOAT, 0)
		return v, Typ[UntypedFloat], v.Kind() == constant.Float || v.Kind() == constant.Int
	case syntax.Rune:
		v := constant.MakeFromLiteral(lit.Value, token.CHAR, 0)
		return v, Typ[UntypedRune], v.Kind() == constant.Int
	case syntax.String:
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return constant.MakeUnknown(), Typ[UntypedString], false
		}
		return constant.MakeString(s), Typ[UntypedString], true
	}
	return constant.MakeUnknown(), Typ[Invalid], false
}

// int64Val returns the value of an integer constant, if it fits.
func int64Val(v constant.Value) (int64, bool) {
	return constant.Int64Val(constant.ToInt(v))
}
