package codegen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"an/build"
)

// Structured control flow (target.arch.structured, comp.gen.structured).
// On an architecture such as WebAssembly a branch does not go to an
// address: it leaves an enclosing block, continuing after its end, or
// repeats an enclosing loop. The code generator places the code of each
// routine in blocks and loops with the method of N. Ramsey, "Beyond
// Relooper" (2022): the parts of the routine between labels are the nodes
// of its control flow graph; a node that is the target of a label in a
// template, or of more than one edge from before it, is a merge node,
// whose code follows a block that the branches to it leave; a node that
// is the target of an edge from after it is a loop header, whose code is
// in a loop that those branches repeat. Each label operand of a template
// becomes the number of blocks between the branch and the target block.

// cfgNode is a node of the control flow graph of a routine.
type cfgNode struct {
	label string
	items []item
	succ  []int // the targets of the labels in its templates
	fall  int   // the next node if control can fall through to it, or -1
	rpo   int   // the reverse postorder number, -1 if unreachable
	idom  int
	loop  bool // a loop header
	merge bool
}

// structure places the items of a routine, which starts with its label,
// in blocks and loops, and replaces their label operands by depths.
func (fn *fnGen) structure(code []item) []item {
	g := fn.g
	if len(code) == 0 {
		return code
	}
	head := code[0]
	head.unit = true
	var nodes []*cfgNode
	cur := &cfgNode{fall: -1}
	nodes = append(nodes, cur)
	index := map[string]int{}
	for _, it := range code[1:] {
		if it.label != "" {
			cur = &cfgNode{label: it.label, fall: -1}
			index[it.label] = len(nodes)
			nodes = append(nodes, cur)
			continue
		}
		cur.items = append(cur.items, it)
	}
	// The edges: the labels of the routine in the templates, and falling
	// through to the next node.
	explicit := map[int]bool{}
	for i, n := range nodes {
		for _, it := range n.items {
			for _, m := range symRef.FindAllStringSubmatch(it.text, -1) {
				if j, ok := index[m[1]]; ok {
					n.succ = append(n.succ, j)
					explicit[j] = true
				}
			}
		}
		last := len(n.items) == 0 || !n.items[len(n.items)-1].term
		if last && i+1 < len(nodes) {
			n.fall = i + 1
		}
	}
	succs := func(n *cfgNode) []int {
		s := append([]int(nil), n.succ...)
		if n.fall >= 0 {
			s = append(s, n.fall)
		}
		return s
	}
	// Reverse postorder from the entry.
	for _, n := range nodes {
		n.rpo = -1
	}
	var order []int
	visited := make([]bool, len(nodes))
	type frame struct{ n, k int }
	stack := []frame{{0, 0}}
	visited[0] = true
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		ss := succs(nodes[top.n])
		if top.k < len(ss) {
			s := ss[top.k]
			top.k++
			if !visited[s] {
				visited[s] = true
				stack = append(stack, frame{s, 0})
			}
			continue
		}
		order = append(order, top.n)
		stack = stack[:len(stack)-1]
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	for k, n := range order {
		nodes[n].rpo = k
	}
	// Dominators (Cooper, Harvey and Kennedy).
	preds := make([][]int, len(nodes))
	for i, n := range nodes {
		if n.rpo < 0 {
			continue
		}
		for _, s := range succs(n) {
			preds[s] = append(preds[s], i)
		}
	}
	for _, n := range nodes {
		n.idom = -1
	}
	nodes[0].idom = 0
	intersect := func(a, b int) int {
		for a != b {
			for nodes[a].rpo > nodes[b].rpo {
				a = nodes[a].idom
			}
			for nodes[b].rpo > nodes[a].rpo {
				b = nodes[b].idom
			}
		}
		return a
	}
	for changed := true; changed; {
		changed = false
		for _, i := range order[1:] {
			d := -1
			for _, p := range preds[i] {
				if nodes[p].idom < 0 {
					continue
				}
				if d < 0 {
					d = p
				} else {
					d = intersect(p, d)
				}
			}
			if d >= 0 && nodes[i].idom != d {
				nodes[i].idom = d
				changed = true
			}
		}
	}
	dominates := func(a, b int) bool {
		for {
			if a == b {
				return true
			}
			if b == 0 {
				return false
			}
			b = nodes[b].idom
		}
	}
	// Loop headers and merge nodes.
	forward := make([]int, len(nodes))
	for i, n := range nodes {
		if n.rpo < 0 {
			continue
		}
		for _, s := range succs(n) {
			if nodes[s].rpo <= n.rpo {
				if !dominates(s, i) {
					fn.errorf("codegen: irreducible control flow, which architecture %s cannot express (target.arch.structured)", g.arch.Name)
					return code
				}
				nodes[s].loop = true
				continue
			}
			forward[s]++
		}
	}
	for i, n := range nodes {
		if n.rpo < 0 {
			continue
		}
		fwdExplicit := false
		for _, p := range preds[i] {
			for _, s := range nodes[p].succ {
				if s == i && nodes[p].rpo < n.rpo {
					fwdExplicit = true
				}
			}
		}
		n.merge = forward[i] >= 2 || fwdExplicit && explicit[i]
	}
	children := make([][]int, len(nodes))
	for _, i := range order[1:] {
		if nodes[i].merge {
			children[nodes[i].idom] = append(children[nodes[i].idom], i)
		}
	}
	for _, c := range children {
		sort.Slice(c, func(a, b int) bool { return nodes[c[a]].rpo > nodes[c[b]].rpo })
	}
	// Emit.
	type ctxEntry struct {
		node int
		loop bool
	}
	out := []item{head}
	emitOp := func(op string, labels map[string]string) []string {
		lines, ok := g.selectRule(op, 0, 0, nil, labels)
		if !ok {
			fn.errorf("architecture %s has no instruction selection rule for operation %s (target.arch.structured)", g.arch.Name, op)
		}
		return lines
	}
	structural := func(op string) {
		for _, l := range emitOp(op, nil) {
			out = append(out, item{text: l, fn: head.fn})
		}
	}
	open := map[string]bool{}
	for _, m := range g.arch.ABI.Structured.Open {
		open[strings.ToLower(m)] = true
	}
	end := strings.ToLower(g.arch.ABI.Structured.End)
	depth := func(ctx []ctxEntry, target int, loop bool) int {
		for k := len(ctx) - 1; k >= 0; k-- {
			if ctx[k].node == target && ctx[k].loop == loop {
				return len(ctx) - 1 - k
			}
		}
		fn.errorf("codegen: no enclosing block for a branch to %s (target.arch.structured)", nodes[target].label)
		return 0
	}
	// resolve replaces the labels of the routine in text by depths.
	resolve := func(text string, from int, ctx []ctxEntry, extra int) string {
		return symRef.ReplaceAllStringFunc(text, func(m string) string {
			sm := symRef.FindStringSubmatch(m)
			j, ok := index[sm[1]]
			if !ok {
				return m
			}
			back := nodes[j].rpo <= nodes[from].rpo
			return strconv.Itoa(extra + depth(ctx, j, back))
		})
	}
	var doTree func(x int, ctx []ctxEntry)
	var nodeWithin func(x int, ys []int, ctx []ctxEntry)
	doTree = func(x int, ctx []ctxEntry) {
		if nodes[x].loop {
			structural("loop")
			nodeWithin(x, children[x], append(append([]ctxEntry(nil), ctx...), ctxEntry{x, true}))
			structural("end")
			return
		}
		nodeWithin(x, children[x], ctx)
	}
	nodeWithin = func(x int, ys []int, ctx []ctxEntry) {
		if len(ys) > 0 {
			structural("block")
			nodeWithin(x, ys[1:], append(append([]ctxEntry(nil), ctx...), ctxEntry{ys[0], false}))
			structural("end")
			doTree(ys[0], ctx)
			return
		}
		n := nodes[x]
		extra := 0
		for _, it := range n.items {
			mn := strings.ToLower(strings.Fields(it.text)[0])
			it.text = resolve(it.text, x, ctx, extra)
			out = append(out, it)
			switch {
			case open[mn]:
				extra++
			case mn == end:
				extra--
			}
		}
		if extra != 0 {
			fn.errorf("codegen: a template of architecture %s leaves %d blocks open (target.arch.structured)", g.arch.Name, extra)
		}
		s := n.fall
		switch {
		case s < 0:
		case nodes[s].rpo <= n.rpo, nodes[s].merge:
			back := nodes[s].rpo <= n.rpo
			if !back && len(ctx) > 0 && ctx[len(ctx)-1] == (ctxEntry{s, false}) {
				break // the block ends here
			}
			for _, l := range emitOp("jump", map[string]string{"L": nodes[s].label}) {
				out = append(out, item{text: resolve(l, x, ctx, 0), fn: head.fn})
			}
		default:
			doTree(s, ctx)
		}
	}
	doTree(0, nil)
	if len(fn.g.errs) > 0 {
		return code
	}
	return out
}

// linkStructured links a structured image: the code of each routine is a
// function whose address is its index after the imports of the
// platform, and the data are placed at origin.
func (g *gen) linkStructured(origin int) (*build.Image, error) {
	addrs := map[string]int{}
	var funcs [][]int // the items of each function
	entry := 0
	for i, it := range g.code {
		if it.unit {
			if it.label == "entry" {
				entry = len(funcs)
			}
			addrs[it.label] = len(g.desc.Platform.Imports) + len(funcs)
			funcs = append(funcs, nil)
		}
		if len(funcs) > 0 && it.text != "" {
			funcs[len(funcs)-1] = append(funcs[len(funcs)-1], i)
		}
	}
	var data []*dataItem
	for _, w := range []bool{false, true} {
		for _, d := range g.data {
			if d.writable == w {
				data = append(data, d)
			}
		}
	}
	pc := origin
	for _, d := range data {
		pc = roundUp(pc, d.align)
		addrs[d.label] = pc
		pc += len(d.bytes)
	}
	memory := pc
	g.addrs = addrs
	g.sizes = map[string]int{}
	var bodies [][]byte
	for _, f := range funcs {
		var body []byte
		for _, i := range f {
			it := g.code[i]
			var err error
			text := symRef.ReplaceAllStringFunc(it.text, func(m string) string {
				sm := symRef.FindStringSubmatch(m)
				a, ok := addrs[sm[1]]
				if !ok {
					err = fmt.Errorf("codegen: undefined label %s", sm[1])
				}
				add, _ := strconv.Atoi(sm[2])
				return strconv.Itoa(a + add)
			})
			if err != nil {
				return nil, err
			}
			code, err := g.assemble(text, 0)
			if err != nil {
				return nil, fmt.Errorf("%s: %q: %v", it.fn, text, err)
			}
			body = append(body, code...)
			g.sizes[it.fn] += len(code)
		}
		bodies = append(bodies, body)
	}
	var image []byte
	for _, d := range data {
		for len(image)+origin < addrs[d.label] {
			image = append(image, 0)
		}
		b := append([]byte(nil), d.bytes...)
		for _, r := range d.relocs {
			v := addrs[r.sym] + r.add
			for k := 0; k < g.ptr; k++ {
				b[r.off+k] = byte(v >> uint(8*k))
			}
		}
		image = append(image, b...)
	}
	registers := 0
	for _, r := range g.arch.Registers {
		if r.Class != "" {
			registers++
		}
	}
	return &build.Image{Bytes: image, Funcs: bodies, Entry: entry, Memory: memory, Registers: registers}, nil
}
