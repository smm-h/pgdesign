package sqlexpr

// Walk traverses the AST in depth-first order, calling fn for each node.
// If fn returns false, children of that node are not visited.
func Walk(node Node, fn func(Node) bool) {
	if !fn(node) {
		return
	}
	switch n := node.(type) {
	case *ColumnRef:
		// leaf node, no children
	case *BoolLiteral:
		// leaf node
	case *StringLiteral:
		// leaf node
	case *IntLiteral:
		// leaf node
	case *FloatLiteral:
		// leaf node
	case *FuncCall:
		for _, arg := range n.Args {
			Walk(arg, fn)
		}
	case *Cast:
		Walk(n.Expr, fn)
	case *BinaryOp:
		Walk(n.Left, fn)
		Walk(n.Right, fn)
	case *UnaryOp:
		Walk(n.Operand, fn)
	case *ExistsExpr:
		Walk(n.Subquery, fn)
	case *SelectExpr:
		for _, col := range n.Columns {
			Walk(col, fn)
		}
		Walk(n.From, fn)
		if n.Where != nil {
			Walk(n.Where, fn)
		}
	case *ParenExpr:
		Walk(n.Inner, fn)
	case *CaseExpr:
		for _, w := range n.Whens {
			Walk(w.Condition, fn)
			Walk(w.Result, fn)
		}
		if n.Else != nil {
			Walk(n.Else, fn)
		}
	}
}

// CollectColumnRefs walks the AST and returns all ColumnRef nodes found.
func CollectColumnRefs(node Node) []*ColumnRef {
	var refs []*ColumnRef
	Walk(node, func(n Node) bool {
		if cr, ok := n.(*ColumnRef); ok {
			refs = append(refs, cr)
		}
		return true
	})
	return refs
}
