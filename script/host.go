package script

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

type EffectClass uint8

const (
	PureEffect EffectClass = iota
	ReadOnlyEffect
	WriteEffect
	UnknownEffect
)

type Operation struct{ Language, Path, Member string }

func (o Operation) Wire() Value {
	return ListValue(textValue(o.Language), textValue(o.Path), textValue(o.Member))
}

type Schema struct {
	Type     string
	Items    *Schema
	Fields   map[string]Schema
	Required []string
}

func validSchema(s Schema, seen map[visitKey]bool) bool {
	typ := strings.TrimSuffix(s.Type, "?")
	if !strings.Contains(" any null bool number string list object ", " "+typ+" ") || typ == "" {
		return false
	}
	if s.Items != nil {
		key := visitKey{reflect.TypeOf(s.Items), reflect.ValueOf(s.Items).Pointer()}
		if seen[key] {
			return false
		}
		seen[key] = true
		if !validSchema(*s.Items, seen) {
			return false
		}
		delete(seen, key)
	}
	if len(s.Fields) > 0 {
		key := visitKey{reflect.TypeOf(s.Fields), uintptr(reflect.ValueOf(s.Fields).UnsafePointer())}
		if seen[key] {
			return false
		}
		seen[key] = true
		defer delete(seen, key)
		for name, sub := range s.Fields {
			if !utf8.ValidString(name) || !validSchema(sub, seen) {
				return false
			}
		}
	}
	for _, name := range s.Required {
		if !utf8.ValidString(name) {
			return false
		}
	}
	return true
}
func validateContract(c Contract) error {
	if c.Version == "" || !utf8.ValidString(c.Version) || !utf8.ValidString(c.Operation.Path) || c.Effect == PureEffect || c.Effect > UnknownEffect || c.Invoke == nil {
		return diagnostic("E-OPERATION", "invalid operation descriptor")
	}
	if c.Operation.Language != "go" && c.Operation.Language != "python" && c.Operation.Language != "host" {
		return diagnostic("E-OPERATION", "invalid external operation language")
	}
	for _, member := range strings.Split(c.Operation.Member, ".") {
		if !validID(member) {
			return diagnostic("E-OPERATION", "invalid operation member")
		}
	}
	if !validSchema(c.Result, map[visitKey]bool{}) {
		return diagnostic("E-OPERATION", "invalid or cyclic result schema")
	}
	names := map[string]bool{}
	for _, param := range c.Params {
		if !validID(param.Name) || names[param.Name] || !validSchema(param.Schema, map[visitKey]bool{}) {
			return diagnostic("E-OPERATION", "invalid parameter schema")
		}
		names[param.Name] = true
	}
	return nil
}

func (s Schema) Validate(v Value) error {
	if !v.IsData() {
		return diagnostic("E-SCHEMA", "value is not Data")
	}
	if s.Type == "" {
		return diagnostic("E-SCHEMA", "schema must declare a type")
	}
	if err := checkType(v, s.Type); err != nil {
		return diagnostic("E-SCHEMA", err.Error())
	}
	if v.k == List && s.Items != nil {
		for _, x := range v.l {
			if err := s.Items.Validate(x); err != nil {
				return err
			}
		}
	}
	if v.k == Object {
		for _, k := range s.Required {
			if _, ok := v.o[k]; !ok {
				return diagnostic("E-SCHEMA", "missing field "+k)
			}
		}
		keys := make([]string, 0, len(s.Fields))
		for k := range s.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sub := s.Fields[k]
			if x, ok := v.o[k]; ok {
				if err := sub.Validate(x); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type Parameter struct {
	Name     string
	Schema   Schema
	Optional bool
}
type Contract struct {
	Operation    Operation
	Version      string
	Params       []Parameter
	Result       Schema
	Effect       EffectClass
	Concurrent   bool
	Capabilities []string
	Resources    map[string]uint64
	Invoke       func(context.Context, Request) Response
}
type Request struct {
	CallID          CallID
	Operation       Operation
	ContractVersion string
	Positional      []Value
	Named           map[string]Value
}
type Outcome uint8

const (
	ResultOutcome Outcome = iota
	RejectedOutcome
	UncertainOutcome
)

type Response struct {
	Outcome        Outcome
	Value          Value
	Error          *Diagnostic
	Receipt        Value
	KnownCompleted bool
	boundaryFault  bool
}

func Result(v Value) Response {
	return Response{Outcome: ResultOutcome, Value: v, KnownCompleted: true}
}
func Rejected(code, message string) Response {
	return Response{Outcome: RejectedOutcome, Error: diagnostic(code, message)}
}
func Uncertain(message string) Response {
	return Response{Outcome: UncertainOutcome, Error: diagnostic("E-UNCERTAIN", message)}
}

type JournalRecord struct {
	Request     Request
	Fingerprint string
	Response    Response
	KnownFault  bool
	aborted     bool
	admission   bool
}
type journalEntry struct {
	record JournalRecord
	ready  chan struct{}
}

// MemoryJournal implements the core's per-run records, not durable recovery.
type MemoryJournal struct {
	mu      sync.Mutex
	entries map[string]*journalEntry
}

func NewMemoryJournal() *MemoryJournal { return &MemoryJournal{entries: map[string]*journalEntry{}} }
func cloneRequest(r Request) Request {
	r.CallID.Position = r.CallID.Position.Copy()
	r.CallID.EvalPath = r.CallID.EvalPath.Copy()
	r.Positional = append([]Value(nil), r.Positional...)
	n := map[string]Value{}
	for k, v := range r.Named {
		n[k] = v
	}
	r.Named = n
	return r
}
func requestArgs(r Request) Value { return ListValue(ListValue(r.Positional...), object(r.Named)) }
func (j *MemoryJournal) Records() []JournalRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	xs := []JournalRecord{}
	for _, e := range j.entries {
		select {
		case <-e.ready:
			r := e.record
			r.Request = cloneRequest(r.Request)
			r.Response.Error = cloneDiagnostic(r.Response.Error)
			xs = append(xs, r)
		default:
		}
	}
	return xs
}
func (j *MemoryJournal) call(ctx context.Context, r Request, execute func() JournalRecord) (JournalRecord, error) {
	key := identityKey(r.CallID.Wire())
	j.mu.Lock()
	if j.entries == nil {
		j.entries = map[string]*journalEntry{}
	}
	if old, ok := j.entries[key]; ok {
		previous := old.record.Request
		a, _ := Canonical(requestArgs(previous))
		b, _ := Canonical(requestArgs(r))
		if previous.Operation != r.Operation || previous.ContractVersion != r.ContractVersion || string(a) != string(b) {
			j.mu.Unlock()
			return JournalRecord{}, diagnostic("E-REPLAY-DIVERGENCE", "CallID request changed")
		}
		j.mu.Unlock()
		select {
		case <-old.ready:
			if old.record.aborted {
				return JournalRecord{}, errorCut
			}
			return old.record, nil
		case <-ctx.Done():
			return JournalRecord{}, ctx.Err()
		}
	}
	e := &journalEntry{record: JournalRecord{Request: cloneRequest(r)}, ready: make(chan struct{})}
	j.entries[key] = e
	j.mu.Unlock()
	record := execute()
	record.Response.Error = cloneDiagnostic(record.Response.Error)
	j.mu.Lock()
	e.record = record
	if record.aborted {
		delete(j.entries, key)
	}
	close(e.ready)
	j.mu.Unlock()
	if record.aborted {
		return JournalRecord{}, errorCut
	}
	record.Response.Error = cloneDiagnostic(record.Response.Error)
	return record, nil
}

// ResourcePool acquires all named resources atomically. It never holds a
// partial allocation while waiting for another resource.
type ResourcePool struct {
	mu             sync.Mutex
	capacity, used map[string]uint64
	changed        chan struct{}
	queue          []*resourceWaiter
}
type resourceWaiter struct{ need map[string]uint64 }

func NewResourcePool(capacity map[string]uint64) *ResourcePool {
	c := map[string]uint64{}
	for k, v := range capacity {
		c[k] = v
	}
	return &ResourcePool{capacity: c, used: map[string]uint64{}, changed: make(chan struct{})}
}
func (p *ResourcePool) acquire(ctx context.Context, need map[string]uint64) (func(), error) {
	waiter := &resourceWaiter{need: need}
	p.mu.Lock()
	for k, n := range need {
		limit, ok := p.capacity[k]
		if !ok || n > limit {
			p.mu.Unlock()
			return nil, diagnostic("E-RESOURCE", "unavailable resource "+k)
		}
	}
	p.queue = append(p.queue, waiter)
	p.mu.Unlock()
	remove := func() {
		p.mu.Lock()
		for i, w := range p.queue {
			if w == waiter {
				p.queue = append(p.queue[:i], p.queue[i+1:]...)
				break
			}
		}
		close(p.changed)
		p.changed = make(chan struct{})
		p.mu.Unlock()
	}
	for {
		if ctx.Err() != nil {
			remove()
			return nil, ctx.Err()
		}
		p.mu.Lock()
		fits := len(p.queue) > 0 && p.queue[0] == waiter
		for k, n := range need {
			limit, ok := p.capacity[k]
			if !ok || n > limit {
				p.mu.Unlock()
				return nil, diagnostic("E-RESOURCE", "unavailable resource "+k)
			}
			if n > limit-p.used[k] {
				fits = false
			}
		}
		if fits {
			p.queue = p.queue[1:]
			for k, n := range need {
				p.used[k] += n
			}
			close(p.changed)
			p.changed = make(chan struct{})
			p.mu.Unlock()
			return func() {
				p.mu.Lock()
				for k, n := range need {
					p.used[k] -= n
				}
				close(p.changed)
				p.changed = make(chan struct{})
				p.mu.Unlock()
			}, nil
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			remove()
			return nil, ctx.Err()
		}
	}
}

type Host struct {
	Contracts    []Contract
	Authorize    func(context.Context, Request) error
	Resources    *ResourcePool
	Capabilities map[string]bool
}

func cloneSchema(s Schema) Schema {
	s.Required = append([]string(nil), s.Required...)
	if s.Items != nil {
		item := cloneSchema(*s.Items)
		s.Items = &item
	}
	fields := make(map[string]Schema, len(s.Fields))
	for k, value := range s.Fields {
		fields[k] = cloneSchema(value)
	}
	s.Fields = fields
	return s
}
func cloneContract(c Contract) Contract {
	params := make([]Parameter, len(c.Params))
	for i, p := range c.Params {
		p.Schema = cloneSchema(p.Schema)
		params[i] = p
	}
	c.Params = params
	c.Result = cloneSchema(c.Result)
	c.Capabilities = append([]string(nil), c.Capabilities...)
	resources := make(map[string]uint64, len(c.Resources))
	for k, n := range c.Resources {
		if n > 0 {
			resources[k] = n
		}
	}
	c.Resources = resources
	return c
}

func bindContract(c Contract, pos []Value, named map[string]Value) ([]Value, error) {
	if len(pos) > len(c.Params) {
		return nil, diagnostic("E-ARITY", "too many operation arguments")
	}
	vs := make([]Value, len(c.Params))
	assigned := make([]bool, len(c.Params))
	for i, v := range pos {
		vs[i] = v
		assigned[i] = true
	}
	for name, v := range named {
		index := -1
		for i, p := range c.Params {
			if p.Name == name {
				index = i
				break
			}
		}
		if index < 0 || assigned[index] {
			return nil, diagnostic("E-ARITY", "unknown/duplicate operation parameter "+name)
		}
		vs[index] = v
		assigned[index] = true
	}
	for i, p := range c.Params {
		if !assigned[i] {
			if !p.Optional {
				return nil, diagnostic("E-ARITY", "missing operation parameter "+p.Name)
			}
			continue
		}
		if err := p.Schema.Validate(vs[i]); err != nil {
			return nil, err
		}
	}
	return vs, nil
}
func invokeSafely(ctx context.Context, c Contract, r Request) (response Response) {
	defer func() {
		if x := recover(); x != nil {
			if c.Effect == ReadOnlyEffect {
				response = Rejected("E-EFFECT-REJECTED", fmt.Sprint(x))
			} else {
				response = Uncertain(fmt.Sprint(x))
			}
		}
	}()
	return c.Invoke(ctx, cloneRequest(r))
}

// The compiled target checks full contract metadata before running. Native
// implementation addresses are intentionally outside the portable descriptor.
func contractFingerprint(c Contract) string {
	var schema func(Schema) Value
	schema = func(s Schema) Value {
		fields := map[string]Value{}
		for k, v := range s.Fields {
			fields[k] = schema(v)
		}
		required := make([]Value, len(s.Required))
		for i, k := range s.Required {
			required[i] = textValue(k)
		}
		items := NullValue()
		if s.Items != nil {
			items = schema(*s.Items)
		}
		return object(map[string]Value{"type": textValue(s.Type), "items": items, "fields": object(fields), "required": ListValue(required...)})
	}
	params := make([]Value, len(c.Params))
	for i, p := range c.Params {
		params[i] = object(map[string]Value{"name": textValue(p.Name), "schema": schema(p.Schema), "optional": BoolValue(p.Optional)})
	}
	caps := make([]Value, len(c.Capabilities))
	for i, v := range c.Capabilities {
		caps[i] = textValue(v)
	}
	resources := map[string]Value{}
	for k, v := range c.Resources {
		resources[k] = textValue(fmt.Sprint(v))
	}
	return digest(object(map[string]Value{"operation": c.Operation.Wire(), "version": textValue(c.Version), "params": ListValue(params...), "result": schema(c.Result), "effect": number(float64(c.Effect)), "concurrent": BoolValue(c.Concurrent), "capabilities": ListValue(caps...), "resources": object(resources)}))
}
