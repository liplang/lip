package script

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"sort"
	"strconv"
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

// DefaultMaxFrames bounds simultaneously active non-tail function calls per
// leaf evaluation. Proper tail calls reuse a frame and do not consume more.
const DefaultMaxFrames = 10000

// Event storage is independent of the configured admission limit. A bounded
// mailbox provides backpressure; only actual work creates goroutines.
const schedulerEventBuffer = 64

// Amortize graph bookkeeping across a bounded batch, independently of mailbox
// capacity and user parallelism. A smaller mailbox still bounds queued values.
const schedulerEventBatch = 1024

type Options struct {
	RunID string
	// Parallelism limits active leaf evaluations. Zero uses one; negative values
	// are configuration errors. Any positive int is accepted without preallocation.
	Parallelism int
	MaxSteps    int64
	MaxElements int64
	MaxNodes    int
	// MaxFrames selects the non-tail call budget. Zero uses DefaultMaxFrames;
	// positive values override it. Negative values are configuration errors.
	MaxFrames int
	Journal   *MemoryJournal
	After     []After
}

// After adds a completion dependency between nodes, regions, or static sites.
// Target and Requires use the specification's AfterTarget_wire encoding.
type After struct {
	Site, DependsOn  int   // Compatibility shorthand for root statements.
	Target, Requires Value // Strict AfterTarget_wire values; both must be present.
}
type Program struct {
	syntax *syntax
	check  *checker
	host   Host
	build  Value
}
type CompileOptions struct {
	Entry string // Empty selects root Main, otherwise the sole root Flow or implicit main.
}
type Report struct {
	Output Value
	// EntryOutput is the logical {kind:"value",value:Data} / {kind:"void"}
	// success envelope. It is nil on rejection, failure, or cancellation.
	EntryOutput *Value
	Error       *Diagnostic
	Journal     *MemoryJournal
	Nodes       []NodeRecord
	Steps       int64
	// CleanupErrors preserve Host cleanup faults separately from node errors.
	CleanupErrors []*Diagnostic
}
type NodeRecord struct {
	Position   Position
	Status     Status
	Cause      string
	Error      *Diagnostic
	CauseError *Diagnostic
}

func Compile(source string, host Host) (*Program, error) {
	return CompileWithOptions(source, host, CompileOptions{})
}

func CompileWithOptions(source string, host Host, options CompileOptions) (*Program, error) {
	return CompileSourceSet(SourceSet{Root: "main.lip", Sources: map[string]Source{"main.lip": {Version: "local", Text: source}}}, host, options)
}
func (p *Program) BuildInfo() Value { return p.build }
func compileSyntax(s *syntax, host Host, build Value) (*Program, error) {
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
	var err error
	host, err = lowerNative(context.Background(), s, host, nil, nil)
	if err != nil {
		return nil, err
	}
	c, err := analyze(s, host)
	if err != nil {
		return nil, err
	}
	return &Program{syntax: s, check: c, host: host, build: build}, nil
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

// DecodeEntryInput is the strict JSON boundary; RunEntry subsequently checks
// positional/named binding and annotations against the selected signature.
func DecodeEntryInput(data []byte) (Value, error) {
	value, err := DecodeData(data)
	if err != nil {
		return Value{}, diagnostic("E-SCHEMA", "invalid EntryInput: "+err.Error())
	}
	if err := checkEntryInput(value); err != nil {
		return Value{}, err
	}
	return value, nil
}

func checkEntryInput(input Value) error {
	if input.k != Object || len(input.o) != 2 || input.o["positional"].k != List || input.o["named"].k != Object || !input.IsData() {
		return diagnostic("E-SCHEMA", "EntryInput requires exactly positional:list<Data> and named:object<string,Data>")
	}
	return nil
}

func (p *Program) bindEntry(input Value) (map[string]Value, error) {
	if p == nil || p.syntax == nil || p.check == nil || p.syntax.body == nil {
		return nil, diagnostic("E-ENTRY", "no selected entry")
	}
	if err := checkEntryInput(input); err != nil {
		return nil, err
	}
	positional, named := input.o["positional"].l, input.o["named"].o
	if len(positional) > len(p.syntax.params) {
		return nil, diagnostic("E-SCHEMA", "too many positional entry inputs")
	}
	bound := make(map[string]Value, len(p.syntax.params))
	for i, param := range p.syntax.params {
		value, present := named[param.name]
		if i < len(positional) {
			if present {
				return nil, diagnostic("E-SCHEMA", "duplicate entry input "+param.name)
			}
			value, present = positional[i], true
		}
		if !present {
			return nil, diagnostic("E-SCHEMA", "missing entry input "+param.name)
		}
		if err := checkType(value, param.typ); err != nil {
			return nil, diagnostic("E-SCHEMA", "entry input "+param.name+": "+err.Error())
		}
		bound[param.name] = value
	}
	if len(named)+len(positional) != len(bound) {
		return nil, diagnostic("E-SCHEMA", "unknown entry input")
	}
	return bound, nil
}

func (p *Program) RunEntry(ctx context.Context, input Value, opts Options) (Report, error) {
	bound, err := p.bindEntry(input)
	if err != nil {
		return Report{}, err
	}
	return p.runBound(ctx, bound, opts, nil)
}

// A non-nil saved map belongs to an interactive cell. Existing values are
// immutable inputs; successful top-level results are committed after scheduling.
func (p *Program) run(ctx context.Context, inputs map[string]Value, opts Options, saved map[string]Value) (Report, error) {
	bound, err := p.bindEntry(object(map[string]Value{"positional": ListValue(), "named": object(inputs)}))
	if err != nil {
		return Report{}, err
	}
	return p.runBound(ctx, bound, opts, saved)
}

func (p *Program) runBound(ctx context.Context, inputs map[string]Value, opts Options, saved map[string]Value) (report Report, err error) {
	if ctx == nil {
		return Report{}, diagnostic("E-VALUE", "nil context")
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if opts.MaxFrames < 0 {
		return Report{}, diagnostic("E-VALUE", "non-tail call frame budget must be nonnegative")
	}
	if opts.Parallelism < 0 {
		return Report{}, diagnostic("E-VALUE", "parallelism must be nonnegative")
	}
	if opts.Parallelism == 0 {
		opts.Parallelism = 1
	}
	if opts.MaxFrames == 0 {
		opts.MaxFrames = DefaultMaxFrames
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
	info := RunInfo{RunID: opts.RunID, Session: saved != nil}
	var native nativeRun
	if p.host.Native != nil {
		native, err = newNativeRun(ctx, opts.RunID, saved != nil)
		if err != nil {
			return Report{}, err
		}
	}
	lease := RunLease{}
	if p.host.BeginRun != nil {
		lease, err = p.host.BeginRun(ctx, info)
		if err != nil {
			return Report{}, diagnosticPhase(err, "host")
		}
		if lease.Context != nil {
			// Preserve caller cancellation even when a Host provides a separate
			// context for lease metadata. A lease cannot outlive its root run.
			leased, cancel := context.WithCancel(lease.Context)
			stop := context.AfterFunc(ctx, cancel)
			defer func() { stop(); cancel() }()
			ctx = leased
		}
	}
	if p.host.Native != nil {
		ctx = context.WithValue(ctx, nativeRunKey{}, native)
	}
	defer func() {
		if lease.End != nil {
			if cleanup := lease.End(context.WithoutCancel(ctx), RunEnd{RunInfo: info, Error: err, Journal: opts.Journal}); cleanup != nil {
				d := at(cleanup, token{site: -1})
				d.Phase = "host"
				report.CleanupErrors = append(report.CleanupErrors, d)
			}
		}
		if p.host.Native != nil {
			keep := []Value{}
			if saved != nil && err == nil && len(report.CleanupErrors) == 0 {
				keep = append(keep, report.Output)
				for _, value := range saved {
					keep = append(keep, value)
				}
			}
			if cleanup := p.host.Native.endRun(native, keep); cleanup != nil {
				report.CleanupErrors = append(report.CleanupErrors, at(cleanup, token{site: -1}))
			}
		}
		if len(report.CleanupErrors) > 0 && err == nil {
			err = report.CleanupErrors[0]
			report.Output, report.EntryOutput = Value{}, nil
		}
	}()
	r := &runState{program: p, ctx: ctx, opts: opts, journal: opts.Journal, stores: map[string]*claimStore{}, events: make(chan nodeEvent, schedulerEventBuffer), keep: map[*node]bool{}, required: map[*region]bool{}}
	env := newEnvironment(nil)
	for name := range builtinArity {
		env.values[name] = Value{k: Function, f: &function{builtin: name}}
	}
	for name, value := range saved {
		env.values[name] = value
	}
	for name, declaration := range p.syntax.data {
		env.values[name] = declaration.value
	}
	for name, e := range p.syntax.functions {
		if _, retained := saved[name]; p.syntax.cell && retained {
			continue
		}
		env.values[name] = Value{k: Function, f: &function{params: e.params, typ: e.typ, body: e.body, captures: env, nativeResult: e.nativeResult}}
	}
	// Install every function before freezing captures so mutual recursion keeps
	// the original function identities without retaining unrelated globals.
	for name, e := range p.syntax.functions {
		if _, retained := saved[name]; p.syntax.cell && retained {
			continue
		}
		captures, err := env.frozen(freeNames(e))
		if err != nil {
			return Report{}, at(err, e.tok)
		}
		env.values[name].f.captures = captures
	}
	entryEnv := newEnvironment(env)
	for name, value := range inputs {
		entryEnv.values[name] = value
	}
	for name := range p.check.stores {
		r.stores[name] = newClaimStore(opts.RunID)
	}
	entryEnv.stores = r.stores
	r.env = env
	r.entry = &flowInstance{result: p.syntax.result, name: p.syntax.flowName, stores: r.stores}
	r.templates = makeTemplates(p.syntax)
	if err := r.configureAfter(); err != nil {
		return Report{}, err
	}
	r.root = &region{kind: "block", status: Running, instance: r.entry}
	r.attachRegionAfter(r.root)
	r.buildBlock(p.syntax.body, entryEnv, nil, r.root, nil)
	for _, constraint := range opts.After {
		if constraint.Target.k != Null || constraint.Requires.k != Null {
			continue
		}
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
	r.refreshAfter()
	if r.buildErr != nil {
		return Report{}, r.buildErr
	}
	if r.hasCycle() {
		if len(r.afterRules) > 0 {
			return Report{}, diagnostic("E-AFTER-CYCLE", "combined After dependency cycle")
		}
		return Report{}, diagnostic("E-GRAPH-CYCLE", "combined dependency cycle")
	}
	report = r.schedule()
	if report.Error != nil {
		return report, report.Error
	}
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	if saved != nil {
		for name := range p.syntax.data {
			saved[name] = env.values[name]
		}
		for name := range p.syntax.functions {
			saved[name] = env.values[name]
		}
		// A cell's live stores end with its run. Retain only completed Data.
		for name, store := range r.stores {
			saved[name] = store.snapshot()
		}
		for _, work := range r.root.children {
			if n := work.node; n != nil && n.status == Completed {
				if n.stmt.tag == "bind" {
					saved[n.stmt.name] = n.value
				}
				if (n.stmt.tag == "bind" || n.stmt.tag == "eval") && p.syntax.result == "void" {
					report.Output = n.value
				}
			}
		}
	}
	output := object(map[string]Value{"kind": textValue("void")})
	if p.syntax.result != "void" {
		output = object(map[string]Value{"kind": textValue("value"), "value": report.Output})
	}
	report.EntryOutput = &output
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
	permitCancel context.CancelFunc
	wake         flowResult
	pos          Position
	instance     *flowInstance
	waiting      *region
	resume       chan flowResult
	parked       bool
	called       map[int]bool
	localRanges  sync.Map
	stmt         *statement
	env          *environment
	parent       *region
	deps         []dependency
	status       Status
	cause        string
	value        Value
	err          *Diagnostic
	causeErr     *Diagnostic
	effect       EffectClass
	concurrent   bool
	control      string
	cut          atomic.Bool
	ticket       uint64
}
type flowInstance struct {
	name   string
	prefix Position
	result string
	stores map[string]*claimStore
	output Value
}
type flowResult struct {
	release func()
	value   Value
	err     error
}
type flowRequest struct {
	definition *flowDecl
	inputs     map[string]Value
	prefix     Position
	resume     chan flowResult
}
type region struct {
	owner                *node
	instance             *flowInstance
	kind                 string
	stmt                 *statement
	entry                *node
	parent               *region
	children             []dependency
	deps                 []dependency
	status               Status
	cause                string
	causeErr             *Diagnostic
	env                  *environment
	prefix               Position
	loop                 *region
	next                 *big.Int
	source               Value
	sourceLen            int
	released             bool
	expanded, stopped    bool
	active               int
	hasCalls, hasBarrier bool
	writers              map[string]bool
	symbol               *afterTarget
}

func (g *region) work() []dependency {
	if len(g.deps) == 0 {
		return g.children
	}
	return append(append([]dependency(nil), g.deps...), g.children...)
}

type permitRequest struct {
	need   map[string]uint64
	resume chan flowResult
}
type nodeEvent struct {
	permit      *permitRequest
	permitReady bool
	release     func()
	node        *node
	value       Value
	control     string
	err         error
	flow        *flowRequest
}
type runState struct {
	permits        int
	program        *Program
	env            *environment
	entry          *flowInstance
	ctx            context.Context
	opts           Options
	journal        *MemoryJournal
	root           *region
	nodes          []*node
	regions        []*region
	effectNodes    []*node
	barrierNodes   []*node
	callRegions    []*region
	barrierRegions []*region
	stores         map[string]*claimStore
	events         chan nodeEvent
	steps          atomic.Int64
	calls          atomic.Int64
	main           *Diagnostic
	keep           map[*node]bool
	required       map[*region]bool
	running        int
	buildErr       error
	readySeq       uint64
	templates      map[int]*nodeTemplate
	afterRules     []afterRule
	afterTargets   []*afterTarget
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
		if r.buildErr != nil {
			return
		}
		needed := 1
		if s.tag == "for" || s.tag == "if" || s.tag == "match" {
			needed++
		}
		if r.opts.MaxNodes > 0 && needed > r.opts.MaxNodes-len(r.nodes)-len(r.regions) {
			r.buildErr = at(diagnostic("E-BUDGET", "graph node budget exhausted"), s.tok)
			return
		}
		pos := append(prefix.Copy(), Pair{s.tok.site, "0"})
		n := &node{instance: parent.instance, pos: pos, stmt: s, env: env, parent: parent, status: Pending, concurrent: true}
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
			parent.instance.stores[s.name].declaration = n
		}
		if s.tag == "join" {
			n.deps = append(n.deps, dependency{node: parent.instance.stores[s.name].declaration})
		}
		r.nodes = append(r.nodes, n)
		if n.effect != PureEffect {
			r.effectNodes = append(r.effectNodes, n)
			if n.effect != ReadOnlyEffect || !n.concurrent {
				r.barrierNodes = append(r.barrierNodes, n)
			}
		}
		work := dependency{node: n}
		if s.tag == "for" || s.tag == "if" || s.tag == "match" {
			calls, barrier, writers := staticSummary(s)
			g := &region{kind: s.tag, stmt: s, entry: n, instance: parent.instance, parent: parent, status: Running, env: env, prefix: prefix.Copy(), loop: loop, next: new(big.Int), hasCalls: calls, hasBarrier: barrier, writers: writers}
			r.regions = append(r.regions, g)
			if g.hasCalls {
				r.callRegions = append(r.callRegions, g)
				if g.hasBarrier {
					r.barrierRegions = append(r.barrierRegions, g)
				}
			}
			work = dependency{region: g}
		}
		r.attachAfter(n)
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
		for name, store := range parent.instance.stores {
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
		if store := findStore(env, name); store != nil {
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
			if store := findStore(env, x.args[0].expr.value.s); store != nil && store.declaration != nil {
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
		if x.tag == "pipe" {
			expr(x.x)
			bound["$pipe"]++
			expr(x.y)
			bound["$pipe"]--
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
	if n.effect == PureEffect {
		return n.deps
	}
	barrier := n.effect != ReadOnlyEffect || !n.concurrent
	nodes, regions := r.barrierNodes, r.barrierRegions
	if barrier {
		nodes, regions = r.effectNodes, r.callRegions
	}
	var order []dependency
	for _, other := range nodes {
		if other == n || nodeAncestor(other, n) || comparePosition(other.pos, n.pos) >= 0 {
			continue
		}
		order = append(order, dependency{node: other, kind: "order"})
	}
	for _, g := range regions {
		if isAncestor(g, n.parent) || comparePosition(g.entry.pos, n.pos) >= 0 {
			continue
		}
		order = append(order, dependency{region: g, kind: "order"})
	}
	if len(order) == 0 {
		return n.deps
	}
	return append(append([]dependency(nil), n.deps...), order...)
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
	regionVisiting, regionDone := map[*region]bool{}, map[*region]bool{}
	var visit func(*node) bool
	var work func(dependency) bool
	work = func(d dependency) bool {
		if d.node != nil {
			return visit(d.node)
		}
		g := d.region
		if g == nil || regionDone[g] {
			return false
		}
		if regionVisiting[g] {
			return true
		}
		regionVisiting[g] = true
		if visit(g.entry) {
			return true
		}
		for _, child := range g.work() {
			if work(child) {
				return true
			}
		}
		delete(regionVisiting, g)
		regionDone[g] = true
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
		if n.waiting != nil && work(dependency{region: n.waiting}) {
			return true
		}
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
	if r.main == nil {
		return
	}
	r.keep = map[*node]bool{}
	r.required = map[*region]bool{}
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
		if g.symbol != nil {
			for _, future := range g.symbol.future {
				workKeep(dependency{region: future})
			}
		}
		nodeKeep(g.entry)
		for _, child := range g.work() {
			workKeep(child)
		}
	}
	nodeKeep = func(n *node) {
		if n == nil || r.keep[n] {
			return
		}
		r.keep[n] = true
		if n.waiting != nil && n.waiting.causeErr == nil {
			workKeep(dependency{region: n.waiting})
		}
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
	var candidates []*node
	for {
		r.recomputeKeep()
		progress := r.updateRegions()
		r.refreshAfter()
		if len(r.afterRules) > 0 && r.hasCycle() && r.buildErr == nil {
			r.buildErr = diagnostic("E-AFTER-CYCLE", "cycle after dynamic expansion")
		}
		if r.buildErr != nil && r.main == nil {
			d := at(r.buildErr, r.program.syntax.body.tok)
			r.main = d
		}
		r.recomputeKeep()
		candidates = candidates[:0]
		for _, n := range r.nodes {
			if n.status == Running {
				n.cut.Store(!r.allowed(n))
				if n.permitCancel != nil && (!r.allowed(n) || r.ctx.Err() != nil) {
					n.permitCancel()
				}
			}
			if n.status == Pending || n.status == Running && n.parked {
				candidates = append(candidates, n)
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool { return comparePosition(candidates[i].pos, candidates[j].pos) < 0 })
		if r.opts.Parallelism > 1 {
			for _, n := range candidates {
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
			sort.SliceStable(candidates, func(i, j int) bool {
				a, b := candidates[i].ticket, candidates[j].ticket
				if a == 0 {
					return false
				}
				if b == 0 {
					return true
				}
				return a < b
			})
		}
		for _, n := range candidates {
			if n.status == Running && n.parked && n.waiting != nil && (terminal(n.waiting.status) || n.permitCancel == nil && (!r.allowed(n) || r.ctx.Err() != nil)) && r.running < r.opts.Parallelism {
				result := flowResult{value: n.waiting.instance.output}
				if n.waiting.kind == "permit" {
					result = n.wake
					n.wake = flowResult{}
				} else if terminal(n.waiting.status) {
					r.releaseRegion(n.waiting)
					n.waiting.instance.output = Value{}
				}
				if r.ctx.Err() != nil {
					result.err = r.ctx.Err()
				} else if !r.allowed(n) {
					result.err = errorCut
				} else if n.waiting.status != Completed {
					result.err = n.waiting.causeErr
					if result.err == nil {
						result.err = errorCut
					}
				}
				if result.err != nil && result.release != nil {
					result.release()
					result.release = nil
				}
				if n.permitCancel != nil {
					n.permitCancel()
					n.permitCancel = nil
				}
				n.parked = false
				n.waiting = nil
				r.running++
				n.resume <- result
				progress = true
			}
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
						event.err = n.instance.stores[n.stmt.name].join(n.pos, event.value)
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
					} else if n.instance.result != "void" {
						if err := checkType(event.value, n.instance.result); err != nil {
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
		if r.running > 0 || r.permits > 0 {
			r.acceptEvent(<-r.events)
			// Coalesce available completions before reconsidering the graph.
			// Limit each batch so ready work and cancellation are not starved.
		drainEvents:
			for count := 1; count < schedulerEventBatch; count++ {
				select {
				case event := <-r.events:
					r.acceptEvent(event)
				default:
					break drainEvents
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
	sort.SliceStable(r.nodes, func(i, j int) bool { return comparePosition(r.nodes[i].pos, r.nodes[j].pos) < 0 })
	if r.main != nil {
		r.main.Phase = "runtime"
	}
	report := Report{Error: r.main, Journal: r.journal, Steps: r.steps.Load(), Output: r.entry.output}
	for _, n := range r.nodes {
		if n.err != nil {
			n.err.Phase = "runtime"
		}
		report.Nodes = append(report.Nodes, NodeRecord{Position: n.pos.Copy(), Status: n.status, Cause: n.cause, Error: cloneDiagnostic(n.err), CauseError: cloneDiagnostic(n.causeErr)})
	}
	if report.Error != nil || r.ctx.Err() != nil {
		report.Output = Value{}
	}
	return report
}
func (r *runState) acceptEvent(event nodeEvent) {
	if event.permitReady {
		r.permits--
		n := event.node
		n.wake = flowResult{release: event.release, err: event.err}
		n.waiting.status = Completed
		return
	}
	r.running--
	n := event.node
	if event.permit != nil {
		request := event.permit
		n.waiting = &region{kind: "permit", status: Running, instance: n.instance, expanded: true}
		n.resume = request.resume
		n.parked = true
		ctx, cancel := context.WithCancel(r.ctx)
		n.permitCancel = cancel
		r.permits++
		go func() {
			release, err := r.program.host.Resources.acquire(ctx, request.need)
			r.events <- nodeEvent{node: n, permitReady: true, release: release, err: err}
		}()
		return
	}
	if event.flow != nil {
		request := event.flow
		budgetError := r.ctx.Err()
		if budgetError == nil {
			budgetError = r.step()
		}
		if budgetError == nil && r.opts.MaxNodes > 0 && len(r.nodes)+len(r.regions) >= r.opts.MaxNodes {
			budgetError = diagnostic("E-BUDGET", "Flow instance budget exhausted")
		}
		if budgetError != nil {
			r.running++
			request.resume <- flowResult{err: budgetError}
			return
		}
		if n.called == nil {
			n.called = map[int]bool{}
		}
		n.called[request.prefix[len(request.prefix)-1].Site] = true
		instance := &flowInstance{result: request.definition.result, name: request.definition.name, prefix: request.prefix, stores: map[string]*claimStore{}}
		env := newEnvironment(r.env)
		for name, value := range request.inputs {
			env.values[name] = value
		}
		env.stores = instance.stores
		for _, stmt := range request.definition.body.items {
			if stmt.tag == "claim" {
				instance.stores[stmt.name] = newClaimStore(r.opts.RunID)
			}
		}
		g := &region{kind: "flow", owner: n, instance: instance, parent: n.parent, prefix: request.prefix, status: Running, expanded: true}
		r.attachRegionAfter(g)
		r.regions = append(r.regions, g)
		n.waiting, n.resume, n.parked = g, request.resume, true
		r.buildBlock(request.definition.body, env, request.prefix, g, nil)
		if len(r.opts.After) > 0 && r.hasCycle() {
			r.buildErr = at(diagnostic("E-GRAPH-CYCLE", "cycle after Flow expansion"), n.stmt.tok)
		}
		return
	}
	n.value = event.value
	// The completion event transfers ownership back from the evaluator.
	// Closures have independent frozen captures; no evaluator needs this frame.
	n.env = nil
	n.control = event.control
	if event.err != nil {
		if event.err == errorCut {
			n.status, n.cause = Skipped, "error_cut"
		} else if r.ctx.Err() != nil {
			n.status, n.cause = Cancelled, "cancelled"
		} else if d, ok := event.err.(*Diagnostic); ok && len(d.Position) > 0 {
			n.status, n.cause, n.causeErr = Skipped, "dependency_failure", d
		} else {
			n.status = Error
			n.err = at(event.err, n.stmt.tok)
			n.err.Phase = "runtime"
			n.err.Position = n.pos.Copy()
			if r.main == nil || compareErrors(n.err, r.main) < 0 {
				r.main = n.err
			}
		}
	} else {
		n.status = Completed
		if n.stmt.tag == "return" {
			n.instance.output = n.value
		}
		if event.control != "" {
			r.closeIteration(n, event.control)
		}
	}
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
	for _, g := range r.regions {
		if terminal(g.status) {
			r.releaseRegion(g)
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
				g.sourceLen = len(g.source.l)
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
		for _, w := range g.work() {
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
				if len(r.afterRules) > 0 {
					limit = g.sourceLen
				}
			}
			for !g.stopped && active < limit && (g.stmt.expr == nil || g.next.IsInt64() && g.next.Int64() < int64(g.sourceLen)) {
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
				iteration := &region{kind: "iteration", parent: g, status: Running, instance: g.instance, env: local, prefix: prefix, loop: g, expanded: true}
				r.regions = append(r.regions, iteration)
				g.children = append(g.children, dependency{region: iteration})
				r.buildBlock(g.stmt.body, local, prefix, iteration, g)
				g.next.Add(g.next, big.NewInt(1))
				active++
				all = false
				progress = true
			}
			finished := g.stopped || g.stmt.expr != nil && g.next.IsInt64() && g.next.Int64() >= int64(g.sourceLen)
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
			r.releaseRegion(g)
		}
	}
	all := true
	failed := false
	for _, w := range r.root.work() {
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

// Completed lexical scopes cannot export live bindings or stores. Data results
// and frozen closure captures own their values independently. Keep graph and
// range metadata for ordering, After, and diagnostics, but drop local payloads.
// Never mutate an environment or a Value's backing storage: other scopes may
// still hold aliases to the same immutable data.
func (r *runState) releaseRegion(g *region) {
	if g == r.root || g.released || !terminal(g.status) {
		return
	}
	stack := []*region{g}
	var scopes []*region
	var nodes []*node
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current.released {
			continue
		}
		if !terminal(current.status) {
			return
		}
		scopes = append(scopes, current)
		if current.entry != nil {
			nodes = append(nodes, current.entry)
		}
		for _, work := range current.children {
			if work.node != nil {
				nodes = append(nodes, work.node)
			} else {
				stack = append(stack, work.region)
			}
		}
	}
	// Control transfers can mark a region before its active workers drain.
	for _, n := range nodes {
		if !terminal(n.status) {
			return
		}
	}
	for _, n := range nodes {
		n.env = nil
		n.value = Value{}
	}
	for _, scope := range scopes {
		scope.env = nil
		scope.source = Value{}
		scope.released = true
	}
	if g.kind == "flow" {
		g.instance.stores = nil
	}
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
		if p.contract.Operation == (Operation{"lip", "claim", "snapshot"}) &&
			(p.snapshotRun != r.opts.RunID || comparePosition(p.snapshotPrefix, v.node.instance.prefix) != 0) {
			record.admission = true
			record.Response = Rejected("E-RESOURCE", "Claim snapshot proposal belongs to another run or Flow instance")
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
			release, e = v.acquirePermit(p.contract.Resources)
			if e != nil {
				if e == errorCut {
					record.aborted = true
					return record
				}
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
			store := v.node.instance.stores[bound[0].s]
			if store == nil {
				record.admission = true
				record.Response = Rejected("E-RESOURCE", "Claim snapshot store is no longer available")
				return record
			}
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
		callContext := v.ctx
		if p.contract.Version == "lip-native-1" && p.contract.Operation.Language == "host" && p.contract.Operation.Path == "lip.native" {
			// Data hidden behind conservative native provenance still uses
			// this evaluation's allocation, instruction, and cancel budgets.
			callContext = context.WithValue(callContext, nativeDataBudgetKey{}, nativeDataBudget{r.reserveElements, func() error { return v.step(t) }})
		}
		response := invokeSafely(callContext, p.contract, request)
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

func nodeAncestor(a, b *node) bool {
	for g := b.parent; g != nil; g = g.parent {
		if g.owner == a || g.entry == a {
			return true
		}
	}
	return false
}
func findStore(env *environment, name string) *claimStore {
	for ; env != nil; env = env.parent {
		if store := env.stores[name]; store != nil {
			return store
		}
	}
	return nil
}
func (v *evaluator) invokeFlow(e *expression, env *environment) (Value, error) {
	f := v.run.program.syntax.flows[e.name]
	order, err := argumentOrder(&function{params: f.params}, e.args)
	if err != nil {
		return Value{}, err
	}
	values := make([]Value, len(f.params))
	for i, arg := range e.args {
		value, err := v.result(arg.expr, env)
		if err != nil {
			return Value{}, err
		}
		values[order[i]] = value
	}
	inputs := make(map[string]Value, len(f.params))
	for i, param := range f.params {
		value := values[i]
		if !value.IsData() {
			return Value{}, diagnostic("E-SCHEMA", "Flow input must be Data")
		}
		if err := checkType(value, param.typ); err != nil {
			return Value{}, diagnostic("E-SCHEMA", err.Error())
		}
		inputs[param.name] = value
	}
	prefix := append(v.node.pos.Copy(), v.path...)
	prefix = append(prefix, Pair{e.tok.site, "0"})
	request := &flowRequest{definition: f, inputs: inputs, prefix: prefix, resume: make(chan flowResult, 1)}
	v.run.events <- nodeEvent{node: v.node, flow: request}
	result := <-request.resume
	return result.value, result.err
}

// Static templates decode positions without guessing their meaning from numeric
// prefixes. They also retain future producers for symbolic After completion.
type callTemplate struct {
	expression *expression
	loops      []int
}
type nodeTemplate struct {
	statement *statement
	flow      string
	loops     []int
	calls     []callTemplate
}
type afterRule struct{ target, requires *afterTarget }
type pathRange struct {
	owner Position
	path  Position
}
type afterTarget struct {
	kind           string
	site           int
	position       Position
	static         bool
	boundary       *region
	owners, needed []Position
	ranges         []pathRange
	future         []*region
}

func makeTemplates(s *syntax) map[int]*nodeTemplate {
	out := map[int]*nodeTemplate{}
	visited := map[string]bool{}
	var flow func(string, *block)
	var graph func(string, *block, []int)
	graph = func(name string, b *block, loops []int) {
		if b == nil {
			return
		}
		for _, st := range b.items {
			t := &nodeTemplate{statement: st, flow: name, loops: append([]int(nil), loops...)}
			out[st.tok.site] = t
			var expr func(*expression, []int)
			var localBlock func(*block, []int)
			var localStmt func(*statement, []int)
			localStmt = func(st *statement, path []int) {
				expr(st.expr, path)
				bodyPath := path
				if st.tag == "for" {
					bodyPath = append(append([]int(nil), path...), st.tok.site)
				}
				localBlock(st.body, bodyPath)
				localBlock(st.otherwise, path)
				if st.other != nil {
					localStmt(st.other, path)
				}
				for _, a := range st.arms {
					expr(a.guard, path)
					expr(a.result, path)
					localBlock(a.body, path)
				}
			}
			localBlock = func(b *block, path []int) {
				if b == nil {
					return
				}
				for _, st := range b.items {
					localStmt(st, path)
				}
				expr(b.tail, path)
				if b.exit != nil {
					localStmt(b.exit, path)
				}
			}
			expr = func(e *expression, path []int) {
				if e == nil {
					return
				}
				if e.tag == "flow" {
					t.calls = append(t.calls, callTemplate{e, append([]int(nil), path...)})
					if f := s.flows[e.name]; f != nil {
						flow(e.name, f.body)
					}
				}
				if e.tag == "fn" {
					return
				}
				if e.tag == "carry" || e.tag == "comprehension" {
					expr(e.y, path)
					next := append(append([]int(nil), path...), e.tok.site)
					if e.tag == "carry" {
						expr(e.x, path)
						localBlock(e.body, next)
					} else {
						expr(e.x, next)
						expr(e.z, next)
					}
					return
				}
				expr(e.x, path)
				expr(e.y, path)
				expr(e.z, path)
				for _, a := range e.args {
					expr(a.expr, path)
				}
				for _, x := range e.elems {
					expr(x, path)
				}
				for _, f := range e.fields {
					expr(f.expr, path)
				}
				localBlock(e.body, path)
				for _, a := range e.arms {
					expr(a.guard, path)
					expr(a.result, path)
					localBlock(a.body, path)
				}
			}
			expr(st.expr, nil)
			for _, a := range st.arms {
				expr(a.guard, nil)
			}
			childLoops := loops
			if st.tag == "for" {
				childLoops = append(append([]int(nil), loops...), st.tok.site)
			}
			graph(name, st.body, childLoops)
			graph(name, st.otherwise, loops)
			if st.other != nil {
				graph(name, &block{items: []*statement{st.other}}, loops)
			}
			for _, a := range st.arms {
				graph(name, a.body, loops)
			}
		}
	}
	flow = func(name string, b *block) {
		if visited[name] {
			return
		}
		visited[name] = true
		graph(name, b, nil)
	}
	flow(s.flowName, s.body)
	return out
}
func canonicalNatural(s string) bool {
	if s == "0" {
		return true
	}
	if len(s) == 0 || s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !asciiDigit(s[i]) {
			return false
		}
	}
	return true
}
func decodePosition(value Value) (Position, error) {
	if value.k != List {
		return nil, diagnostic("E-AFTER-CYCLE", "position must be a pair list")
	}
	out := Position{}
	for _, pair := range value.l {
		if pair.k != List || len(pair.l) != 2 || pair.l[0].k != String || pair.l[1].k != String || !canonicalNatural(pair.l[0].s) || !canonicalNatural(pair.l[1].s) {
			return nil, diagnostic("E-AFTER-CYCLE", "invalid position pair")
		}
		site, err := strconv.Atoi(pair.l[0].s)
		if err != nil {
			return nil, diagnostic("E-AFTER-CYCLE", "unknown site ordinal")
		}
		out = append(out, Pair{site, pair.l[1].s})
	}
	return out, nil
}
func positionPrefix(prefix, pos Position) bool {
	return len(prefix) <= len(pos) && comparePosition(prefix, pos[:len(prefix)]) == 0
}
func (r *runState) parseAfter(wire Value) (*afterTarget, error) {
	bad := func() (*afterTarget, error) {
		return nil, diagnostic("E-AFTER-CYCLE", "invalid or unknown AfterTarget")
	}
	if wire.k != List || len(wire.l) < 2 || wire.l[0].k != String {
		return bad()
	}
	t := &afterTarget{boundary: &region{kind: "after", status: Pending, expanded: true}}
	t.boundary.symbol = t
	switch wire.l[0].s {
	case "site":
		if len(wire.l) != 3 || wire.l[1].k != String || wire.l[2].k != String || !canonicalNatural(wire.l[2].s) {
			return bad()
		}
		site, err := strconv.Atoi(wire.l[2].s)
		if err != nil {
			return bad()
		}
		t.kind, t.site, t.static = wire.l[1].s, site, true
		valid := false
		for _, n := range r.templates {
			if n.statement.tok.site == site && (t.kind == "node" || t.kind == "loop" && n.statement.tag == "for" || t.kind == "branch" && (n.statement.tag == "if" || n.statement.tag == "match")) {
				valid = true
			}
			for _, call := range n.calls {
				if call.expression.tok.site == site && t.kind == "call" {
					valid = true
				}
			}
		}
		if !valid {
			return bad()
		}
	case "node":
		if len(wire.l) != 2 {
			return bad()
		}
		pos, err := decodePosition(wire.l[1])
		if err != nil {
			return nil, err
		}
		t.kind, t.position = "node", pos
	case "region":
		if len(wire.l) != 3 || wire.l[1].k != String {
			return bad()
		}
		pos, err := decodePosition(wire.l[2])
		if err != nil {
			return nil, err
		}
		t.kind, t.position = wire.l[1].s, pos
		if t.kind != "flow" && t.kind != "loop" && t.kind != "branch" {
			return bad()
		}
	default:
		return bad()
	}
	if !t.static {
		var decode func(string, int) bool
		decode = func(flow string, start int) bool {
			if t.kind == "flow" && start == len(t.position) {
				return true
			}
			for _, node := range r.templates {
				if node.flow != flow {
					continue
				}
				end := start + len(node.loops) + 1
				if end > len(t.position) {
					continue
				}
				valid := true
				for i, site := range node.loops {
					valid = valid && t.position[start+i].Site == site
				}
				last := t.position[end-1]
				if !valid || last.Site != node.statement.tok.site || last.Index != "0" {
					continue
				}
				owner := t.position[:end].Copy()
				if end == len(t.position) {
					if t.kind == "node" || t.kind == "loop" && node.statement.tag == "for" || t.kind == "branch" && (node.statement.tag == "if" || node.statement.tag == "match") {
						t.site = last.Site
						t.needed = append(t.needed, owner)
						return true
					}
					continue
				}
				for _, call := range node.calls {
					callEnd := end + len(call.loops) + 1
					if callEnd > len(t.position) {
						continue
					}
					valid := true
					for i, site := range call.loops {
						valid = valid && t.position[end+i].Site == site
					}
					pair := t.position[callEnd-1]
					if !valid || pair.Site != call.expression.tok.site || pair.Index != "0" {
						continue
					}
					t.owners = append(t.owners, owner)
					t.needed = append(t.needed, owner)
					t.ranges = append(t.ranges, pathRange{owner, t.position[end : callEnd-1].Copy()})
					if t.kind == "flow" && callEnd == len(t.position) {
						t.site = pair.Site
						return true
					}
					if decode(call.expression.name, callEnd) {
						return true
					}
					t.owners = t.owners[:len(t.owners)-1]
					t.needed = t.needed[:len(t.needed)-1]
					t.ranges = t.ranges[:len(t.ranges)-1]
				}
			}
			return false
		}
		if !decode(r.program.syntax.flowName, 0) {
			return bad()
		}
	}
	r.afterTargets = append(r.afterTargets, t)
	return t, nil
}
func (r *runState) configureAfter() error {
	for _, constraint := range r.opts.After {
		if constraint.Target.k == Null && constraint.Requires.k == Null {
			continue
		}
		target, err := r.parseAfter(constraint.Target)
		if err != nil {
			return err
		}
		requires, err := r.parseAfter(constraint.Requires)
		if err != nil {
			return err
		}
		if target.kind == requires.kind && target.static == requires.static &&
			(target.static && target.site == requires.site || !target.static && comparePosition(target.position, requires.position) == 0) {
			return diagnostic("E-AFTER-CYCLE", "After target cannot await itself")
		}
		if !target.static && !requires.static {
			if requires.kind == "flow" && positionPrefix(requires.position, target.position) {
				return diagnostic("E-AFTER-CYCLE", "descendant cannot await its Flow ancestor")
			}
			for _, owner := range target.owners {
				if requires.kind == "node" && comparePosition(owner, requires.position) == 0 {
					return diagnostic("E-AFTER-CYCLE", "descendant cannot await its owner")
				}
			}
		}
		r.afterRules = append(r.afterRules, afterRule{target, requires})
	}
	return nil
}
func targetMatchesNode(t *afterTarget, n *node) bool {
	if t.static {
		return t.site == n.stmt.tok.site && (t.kind == "node" || t.kind == "loop" && n.stmt.tag == "for" || t.kind == "branch" && (n.stmt.tag == "if" || n.stmt.tag == "match"))
	}
	return (t.kind == "node" || t.kind == "loop" || t.kind == "branch") && comparePosition(t.position, n.pos) == 0
}
func targetMatchesFlow(t *afterTarget, g *region) bool {
	if g.kind != "flow" && g.kind != "block" {
		return false
	}
	if t.static {
		return t.kind == "call" && g.kind == "flow" && len(g.prefix) > 0 && g.prefix[len(g.prefix)-1].Site == t.site
	}
	return t.kind == "flow" && comparePosition(t.position, g.prefix) == 0
}
func (r *runState) attachAfter(n *node) {
	for _, rule := range r.afterRules {
		match := targetMatchesNode(rule.target, n)
		for g := n.parent; g != nil && !match; g = g.parent {
			match = targetMatchesFlow(rule.target, g)
		}
		if match {
			n.deps = append(n.deps, dependency{region: rule.requires.boundary, kind: "after"})
		}
	}
}
func (r *runState) attachRegionAfter(g *region) {
	for _, rule := range r.afterRules {
		if targetMatchesFlow(rule.target, g) {
			g.deps = append(g.deps, dependency{region: rule.requires.boundary, kind: "after"})
		}
	}
}
func (r *runState) containsAfter(flow string, t *afterTarget, seen map[string]bool) bool {
	if seen[flow] {
		return false
	}
	seen[flow] = true
	for _, node := range r.templates {
		if node.flow != flow {
			continue
		}
		if node.statement.tok.site == t.site && t.kind != "call" && t.kind != "flow" {
			return true
		}
		for _, call := range node.calls {
			if (t.kind == "call" || t.kind == "flow") && call.expression.tok.site == t.site || r.containsAfter(call.expression.name, t, seen) {
				return true
			}
		}
	}
	return false
}
func (r *runState) bodyContainsAfter(b *block, t *afterTarget) bool {
	if b == nil {
		return false
	}
	found := false
	for _, st := range b.items {
		if st.tok.site == t.site && t.kind != "call" && t.kind != "flow" {
			return true
		}
		template := r.templates[st.tok.site]
		if template != nil {
			for _, call := range template.calls {
				if (t.kind == "call" || t.kind == "flow") && call.expression.tok.site == t.site || r.containsAfter(call.expression.name, t, map[string]bool{}) {
					return true
				}
			}
		}
		found = found || r.bodyContainsAfter(st.body, t) || r.bodyContainsAfter(st.otherwise, t)
		if st.other != nil {
			found = found || r.bodyContainsAfter(&block{items: []*statement{st.other}}, t)
		}
		for _, a := range st.arms {
			found = found || r.bodyContainsAfter(a.body, t)
		}
	}
	return found
}
func (r *runState) refreshAfter() {
	for _, t := range r.afterTargets {
		boundary := t.boundary
		boundary.children = nil
		t.future = nil
		matched := false
		add := func(d dependency) {
			for _, old := range boundary.children {
				if old.node == d.node && old.region == d.region {
					return
				}
			}
			boundary.children = append(boundary.children, d)
		}
		for _, n := range r.nodes {
			if t.kind == "node" && targetMatchesNode(t, n) {
				add(dependency{node: n})
				matched = true
			}
			if terminal(n.status) {
				continue
			}
			if !t.static {
				needed := false
				for _, owner := range t.owners {
					needed = needed || comparePosition(owner, n.pos) == 0
				}
				if !needed {
					continue
				}
			}
			template := r.templates[n.stmt.tok.site]
			if template == nil {
				continue
			}
			for _, call := range template.calls {
				if n.called[call.expression.tok.site] && len(call.loops) == 0 {
					continue
				}
				if (t.kind == "call" || t.kind == "flow") && call.expression.tok.site == t.site || r.containsAfter(call.expression.name, t, map[string]bool{}) {
					add(dependency{node: n})
				}
			}
		}
		groups := append([]*region{r.root}, r.regions...)
		for _, g := range groups {
			if g == nil {
				continue
			}
			if targetMatchesFlow(t, g) || g.entry != nil && t.kind != "node" && t.kind != "flow" && t.kind != "call" && targetMatchesNode(t, g.entry) {
				add(dependency{region: g})
				matched = true
			}
			if g.stmt == nil {
				continue
			}
			if !t.static && g.kind == "for" && g.expanded && g.stmt.expr != nil {
				for _, pos := range t.needed {
					i := len(g.prefix)
					if positionPrefix(g.prefix, pos) && i < len(pos) && pos[i].Site == g.stmt.tok.site {
						index, _ := new(big.Int).SetString(pos[i].Index, 10)
						if index.Cmp(big.NewInt(int64(g.sourceLen))) >= 0 {
							r.buildErr = diagnostic("E-AFTER-CYCLE", "After loop index does not exist")
						}
					}
				}
			}
			if terminal(g.status) {
				continue
			}
			future := !g.expanded || g.kind == "for" && !g.stopped && (g.stmt.expr == nil || !g.next.IsInt64() || g.next.Int64() < int64(g.sourceLen))
			if !future {
				continue
			}
			if !r.bodyContainsAfter(g.stmt.body, t) && !r.bodyContainsAfter(g.stmt.otherwise, t) {
				has := false
				if g.stmt.other != nil {
					has = r.bodyContainsAfter(&block{items: []*statement{g.stmt.other}}, t)
				}
				for _, a := range g.stmt.arms {
					has = has || r.bodyContainsAfter(a.body, t)
				}
				if !has {
					continue
				}
			}
			relevant := t.static
			if !t.static {
				for _, pos := range t.needed {
					if !positionPrefix(g.prefix, pos) {
						continue
					}
					if g.kind == "for" {
						i := len(g.prefix)
						if i >= len(pos) || pos[i].Site != g.stmt.tok.site {
							continue
						}
						if g.expanded && g.stmt.expr != nil {
							index, _ := new(big.Int).SetString(pos[i].Index, 10)
							if index.Cmp(big.NewInt(int64(g.sourceLen))) >= 0 {
								r.buildErr = diagnostic("E-AFTER-CYCLE", "After loop index does not exist")
								continue
							}
							if index.Cmp(g.next) < 0 {
								continue
							}
						}
					}
					relevant = true
				}
			}
			if relevant {
				t.future = append(t.future, g)
				if !g.expanded {
					add(dependency{node: g.entry})
				} else {
					add(dependency{region: g})
				}
			}
		}
		// Runtime source lengths distinguish an impossible local index from a gate
		// that was normally closed. Range publication uses sync.Map.
		if !t.static {
			for _, need := range t.ranges {
				for _, n := range r.nodes {
					if comparePosition(n.pos, need.owner) != 0 {
						continue
					}
					for i, pair := range need.path {
						key := identityKey(need.path[:i].Wire()) + ":" + strconv.Itoa(pair.Site)
						if count, ok := n.localRanges.Load(key); ok {
							index, _ := new(big.Int).SetString(pair.Index, 10)
							if index.Cmp(big.NewInt(int64(count.(int)))) >= 0 {
								r.buildErr = diagnostic("E-AFTER-CYCLE", "After local loop index does not exist")
							}
						}
					}
				}
			}
		}
		boundary.status, boundary.cause, boundary.causeErr = Completed, "", nil
		for _, d := range boundary.children {
			if !terminal(d.status()) {
				boundary.status = Pending
			}
			if d.status() == Error || d.status() == Cancelled || d.status() == Skipped && d.cause() != "break" && d.cause() != "continue" && d.cause() != "unselected_gate" {
				boundary.status = Skipped
				boundary.cause = "dependency_failure"
				if source := d.rootError(); source != nil && (boundary.causeErr == nil || compareErrors(source, boundary.causeErr) < 0) {
					boundary.causeErr = source
				}
			}
		}
		if !matched && len(boundary.children) == 0 {
			boundary.status, boundary.cause = Skipped, "unselected_gate"
		}
	}
}
func (v *evaluator) recordRange(site, count int) {
	if v.node != nil {
		v.node.localRanges.Store(identityKey(v.path.Wire())+":"+strconv.Itoa(site), count)
	}
}

func (v *evaluator) acquirePermit(need map[string]uint64) (func(), error) {
	request := &permitRequest{need: need, resume: make(chan flowResult, 1)}
	v.run.events <- nodeEvent{node: v.node, permit: request}
	result := <-request.resume
	return result.release, result.err
}
