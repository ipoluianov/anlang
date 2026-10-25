package types

import (
	"strings"

	"an/syntax"
)

// exprString returns a short source form of e for diagnostics.
func exprString(e syntax.Expr) string {
	var b strings.Builder
	writeExpr(&b, e)
	return b.String()
}

func writeExpr(b *strings.Builder, e syntax.Expr) {
	switch e := e.(type) {
	case nil:
		b.WriteString("<nil>")
	case *syntax.Name:
		b.WriteString(e.Name)
	case *syntax.BasicLit:
		b.WriteString(e.Value)
	case *syntax.Ellipsis:
		b.WriteString("...")
		if e.Elt != nil {
			writeExpr(b, e.Elt)
		}
	case *syntax.CompositeLit:
		if e.Type != nil {
			writeExpr(b, e.Type)
		}
		b.WriteString("{…}")
	case *syntax.FuncLit:
		b.WriteString("func literal")
	case *syntax.ParenExpr:
		b.WriteString("(")
		writeExpr(b, e.X)
		b.WriteString(")")
	case *syntax.SelectorExpr:
		writeExpr(b, e.X)
		b.WriteString(".")
		b.WriteString(e.Sel.Name)
	case *syntax.IndexExpr:
		writeExpr(b, e.X)
		b.WriteString("[")
		writeExprList(b, e.Indices)
		b.WriteString("]")
	case *syntax.SliceExpr:
		writeExpr(b, e.X)
		b.WriteString("[")
		if e.Low != nil {
			writeExpr(b, e.Low)
		}
		b.WriteString(":")
		if e.High != nil {
			writeExpr(b, e.High)
		}
		if e.Slice3 {
			b.WriteString(":")
			if e.Max != nil {
				writeExpr(b, e.Max)
			}
		}
		b.WriteString("]")
	case *syntax.TypeAssertExpr:
		writeExpr(b, e.X)
		b.WriteString(".(")
		if e.Type == nil {
			b.WriteString("type")
		} else {
			writeExpr(b, e.Type)
		}
		b.WriteString(")")
	case *syntax.CallExpr:
		writeExpr(b, e.Fun)
		b.WriteString("(")
		writeExprList(b, e.Args)
		if e.Ellipsis.IsValid() {
			b.WriteString("...")
		}
		b.WriteString(")")
	case *syntax.StarExpr:
		b.WriteString("*")
		writeExpr(b, e.X)
	case *syntax.UnaryExpr:
		b.WriteString(e.Op.String())
		writeExpr(b, e.X)
	case *syntax.BinaryExpr:
		writeExpr(b, e.X)
		b.WriteString(" " + e.Op.String() + " ")
		writeExpr(b, e.Y)
	case *syntax.KeyValueExpr:
		writeExpr(b, e.Key)
		b.WriteString(": ")
		writeExpr(b, e.Value)
	case *syntax.ArrayType:
		b.WriteString("[")
		if e.Len != nil {
			writeExpr(b, e.Len)
		}
		b.WriteString("]")
		writeExpr(b, e.Elt)
	case *syntax.StructType:
		b.WriteString("struct{…}")
	case *syntax.FuncType:
		b.WriteString("func(…)")
	case *syntax.InterfaceType:
		if len(e.Elems) == 0 {
			b.WriteString("interface{}")
		} else {
			b.WriteString("interface{…}")
		}
	case *syntax.MapType:
		b.WriteString("map[")
		writeExpr(b, e.Key)
		b.WriteString("]")
		writeExpr(b, e.Value)
	case *syntax.ChanType:
		switch e.Dir {
		case syntax.ChanSend:
			b.WriteString("chan<- ")
		case syntax.ChanRecv:
			b.WriteString("<-chan ")
		default:
			b.WriteString("chan ")
		}
		writeExpr(b, e.Value)
	case *syntax.EnumType:
		b.WriteString("enum{…}")
	default:
		b.WriteString("…")
	}
}

func writeExprList(b *strings.Builder, list []syntax.Expr) {
	for i, x := range list {
		if i > 0 {
			b.WriteString(", ")
		}
		writeExpr(b, x)
	}
}
