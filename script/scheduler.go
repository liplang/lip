package script

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"sort"
	"sync"
	"sync/atomic"
)

type Status uint8

const (
	Pending Status = iota
	Running
	Completed
	Skipped
	Error
	Cancelled
)

type Options struct {
	RunID       string
	Parallelism int
	MaxSteps    int64
	MaxElements int64
	MaxNodes    int
	MaxFrames   int
	Journal     *MemoryJournal
	After       []After
}

// After constrains a top-level statement to wait for another top-level
// statement or entire region. Sites are exposed by Program.Sites.
type After struct{ Site, DependsOn int }
type Program struct {
	syntax *syntax
	check  *checker
	host   Host
}
type Report struct {
	Output  Value
	Error   *Diagnostic
	Journal *MemoryJournal
	Nodes   []NodeRecord
	Steps   int64
}
type NodeRecord struct {
	Position   Position
	Status     Status
	Cause      string
	Error      *Diagnostic
	CauseError *Diagnostic
}

func Compile(source string, host Host) (*Program, error) {
	contracts := make([]Contract, len(host.Contracts))
	for i, c := range host.Contracts {
		if err := validateContract(c); err != nil {
			return nil, err
		}
		contracts[i] = cloneContract(c)
	}
	host.Contracts = contracts
	capabilities := make(map[string]bool, len(host.Capabilities))
	for k, granted := range host.Capabilities {
		capabilities[k] = granted
	}
	host.Capabilities = capabilities
	s, err := parse(source)
	if err != nil {
		return nil, err
	}
	host, err = lowerNative(context.Background(), s, host, nil)
	if err != nil {
		return nil, err
	}
	c, err := analyze(s, host)
	if err != nil {
		return nil, err
	}
	return &Program{s, c, host}, nil
}
func (p *Program) Sites() map[int]string {
	out := map[int]string{}
	for _, s := range p.syntax.body.items {
		out[s.tok.site] = s.tag
	}
	return out
}
func (p *Program) Run(ctx context.Context, inputs map[string]Value, opts Options) (Report, error) {
	return p.run(ctx, inputs, opts, nil)
}

// A non-nil saved map belongs to an interactive cell. Existing values are
// immutable inputs; successful top-level results are committed after scheduling.
func (p *Program) run(ctx context.Context, inputs map[string]Value, opts Options, saved map[string]Value) (Report, error) {
	if ctx == nil {
		return Report{}, diagnostic("E-VALUE", "nil context")
	}
	if opts.Parallelism <= 0 {
		opts.Parallelism = 1
	}
	if opts.MaxFrames == 0 {
		opts.MaxFrames = 10000
	}
	if opts.RunID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return Report{}, err
		}
		opts.RunID = hex.EncodeToString(id[:])
	}
	if opts.Journal == nil {
		opts.Journal = NewMemoryJournal()
	}
	r := &runState{program: p, ctx: ctx, opts: opts, journal: opts.Journal, stores: map[string]*claimStore{}, events: make(chan nodeEvent, opts.Parallelism), keep: map[*node]bool{}, required: map[*region]bool{}}
	env := newEnvironment(nil)
	for name := range builtinArity {
		env.values[name] = Value{k: Function, f: &function{builtin: name}}
	}
	for name, value := range saved {
		env.values[name] = value
	}
	for name, e := range p.syntax.functions {
		env.values[name] = Value{k: Function, f: &function{params: e.params, typ: e.typ, body: e.body, captures: env}}
	}
	for _, param := range p.syntax.params {
		x, ok := inputs[param.name]
		if !ok {
			return Report{}, diagnostic("E-SCHEMA", "missing Flow input "+param.name)
		}
		if !x.IsData() {
			return Report{}, diagnostic("E-SCHEMA", "Flow input is not Data")
		}
		if err := checkType(x, param.typ); err != nil {
			return Report{}, diagnostic("E-SCHEMA", err.Error())
		}
		env.values[param.name] = x
	}
	if len(inputs) != len(p.syntax.params) {
		return Report{}, diagnostic("E-SCHEMA", "unknown Flow input")
	}
	for name := range p.check.stores {
		r.stores[name] = newClaimStore(opts.RunID)
	}
	r.root = &region{kind: "block", status: Running}
	r.buildBlock(p.syntax.body, env, nil, r.root, nil)
	for _, constraint := range opts.After {
		a, b := r.topWork(constraint.Site), r.topWork(constraint.DependsOn)
		b.kind = "after"
		if a.node == nil && a.region == nil || b.node == nil && b.region == nil {
			return Report{}, diagnostic("E-AFTER-CYCLE", "After requires existing top-level sites")
		}
		if a.node != nil {
			a.node.deps = append(a.node.deps, b)
		} else {
			a.region.entry.deps = append(a.region.entry.deps, b)
		}
	}
	if r.hasCycle() {
		return Report{}, diagnostic("E-GRAPH-CYCLE", "combined dependency cycle")
	}
	report := r.schedule()
	if report.Error != nil {
		return report, report.Error
	}
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	if saved != nil {
		for name := range p.syntax.functions {
			saved[name] = env.values[name]
		}
		for _, work := range r.root.children {
			if n := work.node; n != nil && n.status == Completed {
				if n.stmt.tag == "bind" {
					saved[n.stmt.name] = n.value
				}
				if n.stmt.tag == "bind" || n.stmt.tag == "eval" {
					report.Output = n.value
				}
			}
		}
	}
	return report, nil
}

type dependency struct {
	node   *node
	region *region
	kind   string
}

func (d dependency) rootError() *Diagnostic {
	if d.node != nil {
		if d.node.err != nil {
			return d.node.err
		}
		return d.node.causeErr
	}
	return d.region.causeErr
}

func (d dependency) status() Status {
	if d.node != nil {
		return d.node.status
	}
	return d.region.status
}
func (d dependency) cause() string {
	if d.node != nil {
		return d.node.cause
	}
	return d.region.cause
}

type node struct {
	pos        Position
	stmt       *statement
	env        *environment
	parent     *region
	deps       []dependency
	status     Status
	cause      string
	value      Value
	err        *Diagnostic
	causeErr   *Diagnostic
	effect     EffectClass
	concurrent bool
	control    string
	cut        atomic.Bool
	ticket     uint64
}
type region struct {
	kind                 string
	stmt                 *statement
	entry                *node
	parent               *region
	children             []dependency
	status               Status
	cause                string
	causeErr             *Diagnostic
	env                  *environment
	prefix               Position
	loop                 *region
	next                 *big.Int
	source               Value
	expanded, stopped    bool
	active               int
	hasCalls, hasBarrier bool
	writers              map[string]bool
}
type nodeEvent struct {
	node    *node
	value   Value
	control string
	err     error
}
type runState struct {
	program  *Program
	ctx      context.Context
	opts     Options
	journal  *MemoryJournal
	root     *region
	nodes    []*node
	regions  []*region
	stores   map[string]*claimStore
	events   chan nodeEvent
	steps    atomic.Int64
	calls    atomic.Int64
	main     *Diagnostic
	keep     map[*node]bool
	required map[*region]bool
	running  int
	buildErr error
	readySeq uint64
}

var errorCut = errors.New("internal error cut")

func (r *runState) step() error {
	steps := r.steps.Add(1)
	if r.opts.MaxSteps > 0 && steps > r.opts.MaxSteps {
		return diagnostic("E-BUDGET", "instruction budget exhausted")
	}
	return nil
}
func (r *runState) reserveElements(n int64) error {
	if n < 0 || n > int64(int(^uint(0)>>1)) || r.opts.MaxElements > 0 && n > r.opts.MaxElements {
		return diagnostic("E-BUDGET", "list allocation exceeds element budget")
	}
	return nil
}
func (r *runState) topWork(site int) dependency {
	for _, w := range r.root.children {
		if w.node != nil && w.node.stmt.tok.site == site {
			return w
		}
		if w.region != nil && w.region.stmt.tok.site == site {
			return w
		}
	}
	return dependency{}
}
func staticSummary(s *statement) (calls, barrier bool, writers map[string]bool) {
	writers = map[string]bool{}
	if s.expr != nil && s.expr.info.effect != PureEffect {
		calls = true
		barrier = s.expr.info.effect != ReadOnlyEffect || !s.expr.info.concurrent
	}
	if s.tag == "join" {
		writers[s.name] = true
	}
	add := func(child *statement) {
		a, b, w := staticSummary(child)
		calls = calls || a
		barrier = barrier || b
		for k := range w {
			writers[k] = true
		}
	}
	if s.body != nil {
		for _, x := range s.body.items {
			add(x)
		}
	}
	if s.otherwise != nil {
		for _, x := range s.otherwise.items {
			add(x)
		}
	}
	if s.other != nil {
		add(s.other)
	}
	for _, a := range s.arms {
		if a.guard != nil && a.guard.info.effect != PureEffect {
			calls = true
		}
		if a.body != nil {
			for _, x := range a.body.items {
				add(x)
			}
		}
	}
	return
}
func hasControl(s *statement) bool {
	if s.tag == "break" || s.tag == "continue" {
		return true
	}
	if s.tag == "for" {
		return false
	}
	if s.body != nil {
		for _, x := range s.body.items {
			if hasControl(x) {
				return true
			}
		}
	}
	if s.otherwise != nil {
		for _, x := range s.otherwise.items {
			if hasControl(x) {
				return true
			}
		}
	}
	if s.other != nil && hasControl(s.other) {
		return true
	}
	for _, a := range s.arms {
		if a.body != nil {
			for _, x := range a.body.items {
				if hasControl(x) {
					return true
				}
			}
		}
	}
	return false
}
func (r *runState) buildBlock(b *block, env *environment, prefix Position, parent, loop *region) {
	controls := []dependency{}
	for _, s := range b.items {
		pos := append(prefix.Copy(), Pair{s.tok.site, "0"})
		n := &node{pos: pos, stmt: s, env: env, parent: parent, status: Pending, concurrent: true}
		if s.expr != nil {
			n.effect = s.expr.info.effect
			n.concurrent = s.expr.info.concurrent
		}
		n.deps = append(n.deps, controls...)
		if parent.entry != nil {
			n.deps = append(n.deps, dependency{node: parent.entry})
		}
		n.deps = append(n.deps, r.expressionDeps(s.expr, env)...)
		if s.tag == "claim" {
			r.stores[s.name].declaration = n
		}
		if s.tag == "join" {
			n.deps = append(n.deps, dependency{node: r.stores[s.name].declaration})
		}
		r.nodes = append(r.nodes, n)
		if r.opts.MaxNodes > 0 && len(r.nodes)+len(r.regions) > r.opts.MaxNodes {
			r.buildErr = at(diagnostic("E-BUDGET", "graph node budget exhausted"), s.tok)
		}
		work := dependency{node: n}
		if s.tag == "for" || s.tag == "if" || s.tag == "match" {
			calls, barrier, writers := staticSummary(s)
			g := &region{kind: s.tag, stmt: s, entry: n, parent: parent, status: Running, env: env, prefix: prefix.Copy(), loop: loop, next: new(big.Int), hasCalls: calls, hasBarrier: barrier, writers: writers}
			r.regions = append(r.regions, g)
			if r.opts.MaxNodes > 0 && len(r.nodes)+len(r.regions) > r.opts.MaxNodes {
				r.buildErr = at(diagnostic("E-BUDGET", "graph instance budget exhausted"), s.tok)
			}
			work = dependency{region: g}
		}
		parent.children = append(parent.children, work)
		if s.tag == "bind" {
			next := newEnvironment(env)
			next.refs[s.name] = n
			env = next
		}
		if s.tag == "claim" {
			next := newEnvironment(env)
			next.refs[s.name] = n
			env = next
		}
		if hasControl(s) {
			controls = append(controls, work)
		}
		for name, store := range r.stores {
			_, _, writers := staticSummary(s)
			if writers[name] {
				store.producers = append(store.producers, work)
			}
		}
	}
}
func (r *runState) expressionDeps(e *expression, env *environment) []dependency {
	if e == nil {
		return nil
	}
	names := freeNames(e)
	deps := []dependency{}
	for name := range names {
		if store, ok := r.stores[name]; ok {
			if store.declaration != nil {
				deps = append(deps, dependency{node: store.declaration})
			}
			deps = append(deps, store.producers...)
			continue
		}
		for frame := env; frame != nil; frame = frame.parent {
			if _, ok := frame.values[name]; ok {
				break
			}
			if n, ok := frame.refs[name]; ok {
				deps = append(deps, dependency{node: n})
				break
			}
		}
	}
	// The literal store argument denotes identity, not a complete read.
	var snapshots func(*expression)
	snapshots = func(x *expression) {
		if x == nil {
			return
		}
		if x.tag == "proposal" && x.name == "claim.snapshot" && len(x.args) == 1 {
			if store := r.stores[x.args[0].expr.value.s]; store != nil && store.declaration != nil {
				deps = append(deps, dependency{node: store.declaration})
			}
		}
		walkExpression(x, snapshots)
	}
	snapshots(e)
	return deps
}
func walkExpression(e *expression, visit func(*expression)) {
	for _, x := range []*expression{e.x, e.y, e.z} {
		if x != nil {
			visit(x)
		}
	}
	for _, a := range e.args {
		visit(a.expr)
	}
	for _, x := range e.elems {
		visit(x)
	}
	for _, f := range e.fields {
		visit(f.expr)
	}
	if e.body != nil {
		walkBlockExpressions(e.body, visit)
	}
	for _, a := range e.arms {
		if a.guard != nil {
			visit(a.guard)
		}
		if a.result != nil {
			visit(a.result)
		}
		if a.body != nil {
			walkBlockExpressions(a.body, visit)
		}
	}
}
func walkBlockExpressions(b *block, visit func(*expression)) {
	for _, s := range b.items {
		walkStatementExpressions(s, visit)
	}
	if b.tail != nil {
		visit(b.tail)
	}
	if b.exit != nil {
		walkStatementExpressions(b.exit, visit)
	}
}
func walkStatementExpressions(s *statement, visit func(*expression)) {
	if s.expr != nil {
		visit(s.expr)
	}
	if s.body != nil {
		walkBlockExpressions(s.body, visit)
	}
	if s.otherwise != nil {
		walkBlockExpressions(s.otherwise, visit)
	}
	if s.other != nil {
		walkStatementExpressions(s.other, visit)
	}
	for _, a := range s.arms {
		if a.guard != nil {
			visit(a.guard)
		}
		if a.body != nil {
			walkBlockExpressions(a.body, visit)
		}
	}
}
func freeNames(e *expression) map[string]bool {
	names := map[string]bool{}
	bound := map[string]int{}
	var expr func(*expression)
	var blockWalk func(*block)
	var stmt func(*statement)
	expr = func(x *expression) {
		if x == nil {
			return
		}
		if x.tag == "id" {
			if bound[x.name] == 0 {
				names[x.name] = true
			}
			return
		}
		if x.tag == "fn" {
			for _, p := range x.params {
				bound[p.name]++
			}
			blockWalk(x.body)
			for _, p := range x.params {
				bound[p.name]--
			}
			return
		}
		if x.tag == "carry" {
			expr(x.x)
			expr(x.y)
			bound[x.state]++
			if x.name != "" {
				bound[x.name]++
			}
			blockWalk(x.body)
			bound[x.state]--
			if x.name != "" {
				bound[x.name]--
			}
			return
		}
		if x.tag == "comprehension" {
			expr(x.y)
			bound[x.name]++
			expr(x.z)
			expr(x.x)
			bound[x.name]--
			return
		}
		for _, child := range []*expression{x.x, x.y, x.z} {
			expr(child)
		}
		for _, a := range x.args {
			expr(a.expr)
		}
		for _, child := range x.elems {
			expr(child)
		}
		for _, f := range x.fields {
			expr(f.expr)
		}
		if x.body != nil {
			blockWalk(x.body)
		}
		for _, a := range x.arms {
			expr(a.guard)
			expr(a.result)
			if a.body != nil {
				blockWalk(a.body)
			}
		}
	}
	stmt = func(s *statement) {
		expr(s.expr)
		if s.tag == "for" && s.name != "" {
			bound[s.name]++
		}
		if s.body != nil {
			blockWalk(s.body)
		}
		if s.tag == "for" && s.name != "" {
			bound[s.name]--
		}
		if s.otherwise != nil {
			blockWalk(s.otherwise)
		}
		if s.other != nil {
			stmt(s.other)
		}
		for _, a := range s.arms {
			expr(a.guard)
			if a.body != nil {
				blockWalk(a.body)
			}
		}
	}
	blockWalk = func(b *block) {
		added := []string{}
		for _, s := range b.items {
			stmt(s)
			if s.tag == "bind" {
				bound[s.name]++
				added = append(added, s.name)
			}
		}
		expr(b.tail)
		if b.exit != nil {
			stmt(b.exit)
		}
		for _, name := range added {
			bound[name]--
		}
	}
	expr(e)
	return names
}
func (r *runState) effectiveDeps(n *node) []dependency {
	deps := append([]dependency(nil), n.deps...)
	if n.effect == PureEffect {
		return deps
	}
	barrier := n.effect != ReadOnlyEffect || !n.concurrent
	for _, other := range r.nodes {
		if other == n || other.effect == PureEffect || comparePosition(other.pos, n.pos) >= 0 {
			continue
		}
		if barrier || other.effect != ReadOnlyEffect || !other.concurrent {
			deps = append(deps, dependency{node: other, kind: "order"})
		}
	}
	for _, g := range r.regions {
		if g.entry == nil || !g.hasCalls || isAncestor(g, n.parent) || comparePosition(g.entry.pos, n.pos) >= 0 {
			continue
		}
		if barrier || g.hasBarrier {
			deps = append(deps, dependency{region: g, kind: "order"})
		}
	}
	return deps
}
func isAncestor(a, b *region) bool {
	for b != nil {
		if a == b {
			return true
		}
		b = b.parent
	}
	return false
}
func (r *runState) hasCycle() bool {
	visiting := map[*node]bool{}
	done := map[*node]bool{}
	var visit func(*node) bool
	var work func(dependency) bool
	work = func(d dependency) bool {
		if d.node != nil {
			return visit(d.node)
		}
		if visit(d.region.entry) {
			return true
		}
		for _, child := range d.region.children {
			if work(child) {
				return true
			}
		}
		return false
	}
	visit = func(n *node) bool {
		if n == nil || done[n] {
			return false
		}
		if visiting[n] {
			return true
		}
		visiting[n] = true
		for _, d := range r.effectiveDeps(n) {
			if work(d) {
				return true
			}
		}
		delete(visiting, n)
		done[n] = true
		return false
	}
	for _, n := range r.nodes {
		if visit(n) {
			return true
		}
	}
	return false
}
func terminal(s Status) bool { return s >= Completed }
func (r *runState) recomputeKeep() {
	r.keep = map[*node]bool{}
	r.required = map[*region]bool{}
	if r.main == nil {
		return
	}
	var nodeKeep func(*node)
	var workKeep func(dependency)
	workKeep = func(d dependency) {
		if d.node != nil {
			nodeKeep(d.node)
			return
		}
		g := d.region
		if r.required[g] {
			return
		}
		r.required[g] = true
		nodeKeep(g.entry)
		for _, child := range g.children {
			workKeep(child)
		}
	}
	nodeKeep = func(n *node) {
		if n == nil || r.keep[n] {
			return
		}
		r.keep[n] = true
		for _, d := range r.effectiveDeps(n) {
			workKeep(d)
		}
	}
	for _, n := range r.nodes {
		if comparePosition(n.pos, r.main.Position) <= 0 {
			nodeKeep(n)
		}
	}
}
func (r *runState) allowed(n *node) bool { return r.main == nil || r.keep[n] }
func (r *runState) schedule() Report {
	for {
		r.recomputeKeep()
		progress := r.updateRegions()
		if r.buildErr != nil && r.main == nil {
			d := at(r.buildErr, r.program.syntax.body.tok)
			r.main = d
		}
		r.recomputeKeep()
		for _, n := range r.nodes {
			if n.status == Running {
				n.cut.Store(!r.allowed(n))
			}
		}
		sort.SliceStable(r.nodes, func(i, j int) bool { return comparePosition(r.nodes[i].pos, r.nodes[j].pos) < 0 })
		if r.opts.Parallelism > 1 {
			for _, n := range r.nodes {
				if n.status != Pending || !r.allowed(n) || n.ticket != 0 {
					continue
				}
				ready := true
				for _, d := range r.effectiveDeps(n) {
					if d.status() != Completed && !(d.status() == Skipped && (d.cause() == "break" || d.cause() == "continue" || d.cause() == "unselected_gate")) {
						ready = false
						break
					}
				}
				if ready {
					r.readySeq++
					n.ticket = r.readySeq
				}
			}
			sort.SliceStable(r.nodes, func(i, j int) bool {
				a, b := r.nodes[i].ticket, r.nodes[j].ticket
				if a == 0 {
					return false
				}
				if b == 0 {
					return true
				}
				return a < b
			})
		}
		for _, n := range r.nodes {
			if n.status != Pending {
				continue
			}
			if r.ctx.Err() != nil {
				n.status, n.cause = Cancelled, "cancelled"
				progress = true
				continue
			}
			if !r.allowed(n) {
				n.status, n.cause = Skipped, "error_cut"
				progress = true
				continue
			}
			ready := true
			for _, d := range r.effectiveDeps(n) {
				s := d.status()
				if s == Error || s == Cancelled || s == Skipped && d.cause() != "unselected_gate" && d.cause() != "break" && d.cause() != "continue" {
					n.status, n.cause = Skipped, "dependency_failure"
					if d.kind == "after" {
						n.cause = "after_failure"
					} else if d.kind == "order" {
						n.cause = "order_failure"
					}
					n.causeErr = d.rootError()
					ready = false
					progress = true
					break
				}
				if !terminal(s) {
					ready = false
				}
			}
			if !ready || r.running >= r.opts.Parallelism {
				continue
			}
			n.status = Running
			r.running++
			progress = true
			go func(n *node) {
				v := &evaluator{run: r, node: n, ctx: r.ctx}
				event := nodeEvent{node: n}
				if n.stmt.tag == "break" || n.stmt.tag == "continue" {
					event.control = n.stmt.tag
				} else if n.stmt.tag == "claim" {
					event.value = Value{}
				} else if n.stmt.tag == "join" {
					event.value, event.err = v.result(n.stmt.expr, n.env)
					if event.err == nil {
						event.err = r.stores[n.stmt.name].join(n.pos, event.value)
					}
				} else if n.stmt.tag == "match" {
					x, e := v.result(n.stmt.expr, n.env)
					event.err = e
					if e == nil {
						a, e := v.selectArm(n.stmt.arms, x, n.env)
						event.err = e
						index := -1
						for i := range n.stmt.arms {
							if a == &n.stmt.arms[i] {
								index = i
							}
						}
						event.value = number(float64(index))
					}
				} else {
					event.value, event.err = v.result(n.stmt.expr, n.env)
				}
				if event.err == nil && n.stmt.tag == "return" {
					if !event.value.IsData() {
						event.err = diagnostic("E-SCHEMA", "Flow output is not Data")
					} else if r.program.syntax.result != "void" {
						if err := checkType(event.value, r.program.syntax.result); err != nil {
							event.err = diagnostic("E-SCHEMA", err.Error())
						}
					}
				}
				if event.err == nil && n.stmt.tag == "if" && event.value.k != Bool {
					event.err = diagnostic("E-TYPE", "if requires bool")
				}
				if event.err == nil && n.stmt.tag == "for" && n.stmt.expr != nil && event.value.k != List {
					event.err = diagnostic("E-TYPE", "for requires list")
				}
				r.events <- event
			}(n)
		}
		if r.running > 0 {
			event := <-r.events
			r.running--
			n := event.node
			n.value = event.value
			n.control = event.control
			if event.err != nil {
				if event.err == errorCut {
					n.status, n.cause = Skipped, "error_cut"
				} else if r.ctx.Err() != nil {
					n.status, n.cause = Cancelled, "cancelled"
				} else {
					n.status = Error
					n.err = at(event.err, n.stmt.tok)
					n.err.Position = n.pos.Copy()
					if r.main == nil || compareErrors(n.err, r.main) < 0 {
						r.main = n.err
					}
				}
			} else {
				n.status = Completed
				if event.control != "" {
					r.closeIteration(n, event.control)
				}
			}
			continue
		}
		if terminal(r.root.status) {
			break
		}
		if !progress {
			r.main = diagnostic("E-GRAPH-CYCLE", "no ready work; graph cannot make progress")
			break
		}
	}
	report := Report{Error: r.main, Journal: r.journal, Steps: r.steps.Load()}
	for _, n := range r.nodes {
		report.Nodes = append(report.Nodes, NodeRecord{Position: n.pos.Copy(), Status: n.status, Cause: n.cause, Error: cloneDiagnostic(n.err), CauseError: cloneDiagnostic(n.causeErr)})
		if n.stmt.tag == "return" && n.status == Completed {
			report.Output = n.value
		}
	}
	if report.Error != nil || r.ctx.Err() != nil {
		report.Output = Value{}
	}
	return report
}
func (r *runState) closeIteration(n *node, control string) {
	g := n.parent
	for g != nil && g.kind != "iteration" {
		g = g.parent
	}
	if g == nil {
		return
	}
	if control == "break" {
		g.parent.stopped = true
	}
	for _, other := range r.nodes {
		if other.status == Pending && isAncestor(g, other.parent) {
			other.status, other.cause = Skipped, control
		}
	}
	for _, child := range r.regions {
		if isAncestor(g, child) && child != g {
			child.status, child.cause = Skipped, control
		}
	}
}
func (r *runState) updateRegions() bool {
	progress := false
	for _, g := range append([]*region(nil), r.regions...) {
		if terminal(g.status) {
			continue
		}
		if !g.expanded {
			if !terminal(g.entry.status) {
				continue
			}
			if g.entry.status != Completed {
				g.status, g.cause = Skipped, "dependency_failure"
				g.causeErr = dependency{node: g.entry}.rootError()
				progress = true
				continue
			}
			g.expanded = true
			progress = true
			s := g.stmt
			switch g.kind {
			case "if":
				if g.entry.value.k != Bool {
					g.entry.status = Error
					g.entry.err = at(diagnostic("E-TYPE", "if requires bool"), s.tok)
					g.entry.err.Position = g.entry.pos.Copy()
					if r.main == nil || compareErrors(g.entry.err, r.main) < 0 {
						r.main = g.entry.err
					}
					g.status = Error
					g.causeErr = g.entry.err
					continue
				}
				if g.entry.value.b {
					r.buildBlock(s.body, newEnvironment(g.env), g.prefix, g, g.loop)
				} else if s.otherwise != nil {
					r.buildBlock(s.otherwise, newEnvironment(g.env), g.prefix, g, g.loop)
				} else if s.other != nil {
					r.buildBlock(&block{items: []*statement{s.other}}, newEnvironment(g.env), g.prefix, g, g.loop)
				}
			case "match":
				index := int(g.entry.value.n)
				if index >= 0 {
					r.buildBlock(s.arms[index].body, newEnvironment(g.env), g.prefix, g, g.loop)
				}
			case "for":
				g.source = g.entry.value
				if s.expr != nil && g.source.k != List {
					g.entry.status = Error
					g.entry.err = at(diagnostic("E-TYPE", "for requires list"), s.tok)
					g.entry.err.Position = g.entry.pos.Copy()
					if r.main == nil || compareErrors(g.entry.err, r.main) < 0 {
						r.main = g.entry.err
					}
					g.status = Error
					g.causeErr = g.entry.err
					continue
				}
			}
		}
		all := true
		failed := false
		active := 0
		for _, w := range g.children {
			if !terminal(w.status()) {
				all = false
				active++
			}
			if w.status() == Error || w.status() == Cancelled || w.status() == Skipped && w.cause() != "break" && w.cause() != "continue" && w.cause() != "unselected_gate" {
				failed = true
				if source := w.rootError(); source != nil && (g.causeErr == nil || compareErrors(source, g.causeErr) < 0) {
					g.causeErr = source
				}
			}
		}
		if g.kind == "for" {
			g.active = active
			if failed {
				g.stopped = true
			}
			limit := 1
			if g.stmt.parallel {
				limit = r.opts.Parallelism
			}
			for !g.stopped && active < limit && (g.stmt.expr == nil || g.next.IsInt64() && g.next.Int64() < int64(len(g.source.l))) {
				if r.ctx.Err() != nil {
					g.stopped, g.cause = true, "cancelled"
					break
				}
				budgetError := r.step()
				if budgetError == nil && r.opts.MaxNodes > 0 && len(r.nodes)+len(r.regions) >= r.opts.MaxNodes {
					budgetError = diagnostic("E-BUDGET", "graph instance budget exhausted")
				}
				if budgetError != nil {
					d := at(budgetError, g.stmt.tok)
					d.Position = g.entry.pos.Copy()
					if r.main == nil || compareErrors(d, r.main) < 0 {
						r.main = d
					}
					g.stopped, g.cause = true, "dependency_failure"
					progress = true
					break
				}
				prefix := append(g.prefix.Copy(), Pair{g.stmt.tok.site, g.next.String()})
				if r.main != nil && !r.required[g] && comparePosition(prefix, r.main.Position) > 0 {
					g.stopped = true
					break
				}
				local := newEnvironment(g.env)
				if g.stmt.name != "" {
					local.values[g.stmt.name] = g.source.l[g.next.Int64()]
				}
				iteration := &region{kind: "iteration", parent: g, status: Running, env: local, prefix: prefix, loop: g, expanded: true}
				r.regions = append(r.regions, iteration)
				g.children = append(g.children, dependency{region: iteration})
				r.buildBlock(g.stmt.body, local, prefix, iteration, g)
				g.next.Add(g.next, big.NewInt(1))
				active++
				all = false
				progress = true
			}
			finished := g.stopped || g.stmt.expr != nil && g.next.IsInt64() && g.next.Int64() >= int64(len(g.source.l))
			if !finished {
				all = false
			}
		}
		if all {
			g.status = Completed
			if g.cause == "cancelled" {
				g.status = Cancelled
			} else if failed || g.cause != "" {
				g.status = Skipped
				g.cause = "dependency_failure"
			}
			progress = true
		}
	}
	all := true
	failed := false
	for _, w := range r.root.children {
		if !terminal(w.status()) {
			all = false
		}
		if w.status() == Error || w.status() == Cancelled || w.status() == Skipped && w.cause() != "break" && w.cause() != "continue" && w.cause() != "unselected_gate" {
			failed = true
		}
	}
	if all {
		r.root.status = Completed
		if failed {
			r.root.status = Skipped
			r.root.cause = "dependency_failure"
		}
		progress = true
	}
	return progress
}

type claimStore struct {
	mu          sync.Mutex
	runID       string
	atoms       map[string]Value
	variants    map[string][]Value
	positions   map[string]Position
	digests     map[string]string
	conflicted  bool
	declaration *node
	producers   []dependency
}

func newClaimStore(runID string) *claimStore {
	return &claimStore{runID: runID, atoms: map[string]Value{}, variants: map[string][]Value{}, positions: map[string]Position{}, digests: map[string]string{}}
}
func (s *claimStore) join(pos Position, v Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !v.IsData() {
		return diagnostic("E-SCHEMA", "Claim contribution must be Data")
	}
	wire := joinWire(s.runID, pos)
	key := identityKey(wire)
	id := digest(wire)
	if old, ok := s.digests[id]; ok && old != key {
		return diagnostic("E-ID-COLLISION", "JoinDigest collision")
	}
	if old, ok := s.atoms[key]; ok {
		same, _ := Equal(old, v)
		if !same {
			found := false
			for _, variant := range s.variants[key] {
				eq, _ := Equal(variant, v)
				found = found || eq
			}
			if !found {
				s.variants[key] = append(s.variants[key], v)
			}
			s.conflicted = true
			return diagnostic("E-CLAIM-CONFLICT", "same JoinID has different value")
		}
		return nil
	}
	s.atoms[key] = v
	s.variants[key] = []Value{v}
	s.positions[key] = pos.Copy()
	s.digests[id] = key
	return nil
}
func (s *claimStore) conflictError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conflicted {
		return diagnostic("E-CLAIM-CONFLICT", "store contains an identity conflict")
	}
	return nil
}
func (s *claimStore) snapshot() Value {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}
func (s *claimStore) read() (Value, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conflicted {
		return Value{}, diagnostic("E-CLAIM-CONFLICT", "store contains an identity conflict")
	}
	return s.snapshotLocked(), nil
}
func (s *claimStore) snapshotLocked() Value {
	keys := make([]string, 0, len(s.atoms))
	for k := range s.atoms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return comparePosition(s.positions[keys[i]], s.positions[keys[j]]) < 0 })
	xs := make([]Value, len(keys))
	for i, key := range keys {
		pos := s.positions[key]
		xs[i] = object(map[string]Value{"id": textValue(digest(joinWire(s.runID, pos))), "run_id": textValue(s.runID), "position": pos.Wire(), "value": s.atoms[key]})
	}
	return ListValue(xs...)
}
func (r *runState) perform(v *evaluator, t token, p *proposal) (Value, error) {
	id := CallID{r.opts.RunID, v.node.pos.Copy(), v.path.Copy(), t.site}
	request := Request{id, p.contract.Operation, p.contract.Version, p.positional, p.named}
	record, err := r.journal.call(v.ctx, request, func() JournalRecord {
		record := JournalRecord{Request: cloneRequest(request), Fingerprint: digest(requestArgs(request))}
		reject := func(err error) JournalRecord {
			record.admission = true
			record.Response = Rejected("E-CAPABILITY", err.Error())
			return record
		}
		if _, err := bindContract(p.contract, p.positional, p.named); err != nil {
			record.admission = true
			record.Response = Rejected("E-SCHEMA", err.Error())
			return record
		}
		if err := v.ctx.Err(); err != nil {
			return reject(err)
		}
		if r.program.host.Authorize != nil {
			if err := r.program.host.Authorize(v.ctx, cloneRequest(request)); err != nil {
				return reject(err)
			}
		}
		for _, capability := range p.contract.Capabilities {
			if !r.program.host.Capabilities[capability] {
				record.admission = true
				record.Response = Rejected("E-CAPABILITY", "missing capability "+capability)
				return record
			}
		}
		release := func() {}
		if len(p.contract.Resources) > 0 {
			if r.program.host.Resources == nil {
				record.admission = true
				record.Response = Rejected("E-RESOURCE", "no resource pool")
				return record
			}
			var e error
			release, e = r.program.host.Resources.acquire(v.ctx, p.contract.Resources)
			if e != nil {
				record.admission = true
				record.Response = Rejected("E-RESOURCE", e.Error())
				return record
			}
		}
		defer release()
		if v.node.cut.Load() {
			record.aborted = true
			return record
		}
		if p.contract.Operation == (Operation{"lip", "claim", "snapshot"}) {
			bound, _ := bindContract(p.contract, p.positional, p.named)
			store := r.stores[bound[0].s]
			value, e := store.read()
			if e != nil {
				record.admission = true
				record.Response = Rejected("E-CLAIM-CONFLICT", e.Error())
			} else {
				record.Response = Result(value)
			}
			return record
		}
		r.calls.Add(1)
		response := invokeSafely(v.ctx, p.contract, request)
		if response.Outcome == RejectedOutcome && response.KnownCompleted && response.Error != nil && (response.Error.Code == "E-SCHEMA" || response.Error.Code == "E-VALUE") {
			record.KnownFault = true
		}
		if response.Outcome == ResultOutcome {
			if e := p.contract.Result.Validate(response.Value); e != nil {
				response.Outcome = RejectedOutcome
				response.Error = diagnostic("E-SCHEMA", e.Error())
				response.KnownCompleted = true
				record.KnownFault = true
			}
		}
		record.Response = response
		return record
	})
	if err != nil {
		return Value{}, err
	}
	response := record.Response
	if response.Outcome == ResultOutcome {
		return response.Value, nil
	}
	if response.Outcome == UncertainOutcome {
		message := "provider outcome uncertain"
		if response.Error != nil {
			message = response.Error.Message
		}
		return Value{}, diagnostic("E-UNCERTAIN", message)
	}
	if response.Error == nil {
		return Value{}, diagnostic("E-EFFECT-REJECTED", "provider rejected operation")
	}
	e := *response.Error
	if !record.KnownFault && !record.admission && !response.boundaryFault {
		e.Code = "E-EFFECT-REJECTED"
	}
	return Value{}, &e
}
