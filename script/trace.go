package script

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// TraceEvent is a detached diagnostic snapshot, never a language value or an
// operation receipt. Text is bounded and may contain opaque value markers.
type TraceEvent struct {
	Kind, Level, Name, Text string
	RunID, SourceID         string
	Site, Line, Column      int
	Position, EvalPath      Position
}

func (e TraceEvent) String() string {
	switch e.Kind {
	case "log":
		return "[" + e.Level + "] " + e.Text
	case "telemetry":
		return "[telemetry " + e.Name + "] " + e.Text
	default:
		return e.Text
	}
}

// TraceBuffer provides bounded, one-way delivery. Consumers cannot acknowledge
// events or call back into evaluation. Closing or filling it only drops events.
// A buffer may be shared by runs and closed concurrently with their emission.
// The zero value is disabled: it drops events and Events returns a nil channel.
type TraceBuffer struct {
	mu      sync.RWMutex
	events  chan TraceEvent
	closed  bool
	dropped atomic.Uint64
}

func NewTraceBuffer(capacity int) (*TraceBuffer, error) {
	if capacity < 0 || capacity > 65536 {
		return nil, fmt.Errorf("trace capacity must be between 0 and 65536")
	}
	if capacity == 0 {
		capacity = 256
	}
	return &TraceBuffer{events: make(chan TraceEvent, capacity)}, nil
}

func (b *TraceBuffer) Events() <-chan TraceEvent { return b.events }
func (b *TraceBuffer) Dropped() uint64           { return b.dropped.Load() }
func (b *TraceBuffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		if b.events != nil {
			close(b.events)
		}
	}
}
func (b *TraceBuffer) emit(e TraceEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.closed {
		select {
		case b.events <- e:
			return
		default:
		}
	}
	b.dropped.Add(1)
}

// StartTraceOutput is the optional console receiver used by command-line
// profiles. Writer faults/panics never reach evaluation. Final draining is
// bounded; an unresponsive Writer cannot delay a command indefinitely.
func StartTraceOutput(writer io.Writer) (*TraceBuffer, func()) {
	b, _ := NewTraceBuffer(0)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		failed := false
		for e := range b.Events() {
			if !failed {
				_, err := fmt.Fprintln(writer, e.String())
				failed = err != nil
			}
		}
	}()
	var once sync.Once
	return b, func() {
		once.Do(func() {
			b.Close()
			timer := time.NewTimer(100 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
			}
		})
	}
}

// traceText bounds work as well as retained bytes. Large objects are summarized
// before sorting keys; closures and native objects are never traversed.
func traceText(value Value) string {
	var out strings.Builder
	const limit = 4096
	write := func(s string) {
		remaining := limit - out.Len()
		if len(s) > remaining {
			s = s[:remaining]
			for !utf8.ValidString(s) {
				s = s[:len(s)-1]
			}
		}
		out.WriteString(s)
	}
	var render func(Value, int)
	render = func(v Value, depth int) {
		if out.Len() >= limit {
			return
		}
		if depth > 8 {
			write("<depth limit>")
			return
		}
		switch v.k {
		case Null:
			write("null")
		case Bool:
			write(strconv.FormatBool(v.b))
		case Number:
			write(strconv.FormatFloat(v.n, 'g', -1, 64))
		case String:
			text := v.s
			if len(text) > limit {
				text = text[:limit-3]
				for !utf8.ValidString(text) {
					text = text[:len(text)-1]
				}
				text += "..."
			}
			if depth == 0 {
				write(text)
			} else {
				write(strconv.Quote(text))
			}
		case Char:
			text := string(v.r)
			if depth == 0 {
				write(text)
			} else {
				write(strconv.Quote(text))
			}
		case Function:
			write("<function>")
		case Effect:
			write("<effect>")
		case List:
			write("[")
			for i, item := range v.l {
				if i >= 32 || out.Len() >= limit {
					write("...")
					break
				}
				if i != 0 {
					write(",")
				}
				render(item, depth+1)
			}
			write("]")
		case Object:
			if ref, native := v.o[nativeReferenceKey]; native && ref.k == Object {
				write("<native reference>")
				return
			}
			if len(v.o) > 32 {
				write("<object: " + strconv.Itoa(len(v.o)) + " fields>")
				return
			}
			keys := make([]string, 0, len(v.o))
			for key := range v.o {
				if len(key) > limit {
					write("<object: long keys>")
					return
				}
				keys = append(keys, key)
			}
			sort.Strings(keys)
			write("{")
			for i, key := range keys {
				if out.Len() >= limit {
					break
				}
				if i != 0 {
					write(",")
				}
				// Keys are values too; bound them before quoting.
				render(textValue(key), depth+1)
				write(":")
				render(v.o[key], depth+1)
			}
			write("}")
		}
	}
	render(value, 0)
	return out.String()
}

func (v *evaluator) emitTrace(kind, level, name string, value Value, tok token) {
	if v.run == nil || v.run.opts.Trace == nil {
		return
	}
	e := TraceEvent{Kind: kind, Level: traceText(textValue(level)), Name: traceText(textValue(name)),
		Text: traceText(value), RunID: traceText(textValue(v.run.opts.RunID)), SourceID: traceText(textValue(tok.source)),
		Site: tok.site, Line: tok.line, Column: tok.column, EvalPath: v.path.Copy()}
	if v.node != nil {
		e.Position = v.node.pos.Copy()
	}
	v.run.opts.Trace.emit(e)
}
