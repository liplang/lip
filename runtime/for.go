package runtime

import (
	"context"
	"errors"
	"fmt"
)

// Private signals are consumed by the nearest ForEach, through any wrapping
// added by the body graph. They never escape as user-visible execution errors.
var forBreak = errors.New("break")
var forContinue = errors.New("continue")

// SourceError retains a failing statement's position through loop and graph
// error wrapping. The innermost statement takes precedence over its containers.
type SourceError struct {
	Line, Column int
	Err          error
}

func (e *SourceError) Error() string { return fmt.Sprintf("%d:%d: %v", e.Line, e.Column, e.Err) }
func (e *SourceError) Unwrap() error { return e.Err }

func WithSource(err error, line, column int) error {
	if err == nil {
		return nil
	}
	if _, _, found := SourcePosition(err); found {
		return err
	}
	return &SourceError{Line: line, Column: column, Err: err}
}

func SourcePosition(err error) (line, column int, found bool) {
	var source *SourceError
	if errors.As(err, &source) {
		return source.Line, source.Column, true
	}
	return 0, 0, false
}

func BreakLoop() Result    { return Failed(forBreak) }
func ContinueLoop() Result { return Failed(forContinue) }

// ForEach visits a bounded list in source order, awaiting the entire body
// before starting another iteration. Results are discarded, never collected.
func ForEach(ctx context.Context, source Value, body func(context.Context, Value) Result) (Value, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("for has no body evaluator")
	}
	items, err := listInput(source)
	if err != nil {
		return nil, fmt.Errorf("for source: %w", err)
	}
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := ResolveValue(ctx, body(ctx, item)); err != nil {
			if errors.Is(err, forBreak) {
				return nil, nil
			}
			if errors.Is(err, forContinue) {
				continue
			}
			return nil, fmt.Errorf("for iteration %d: %w", index, err)
		}
	}
	return nil, ctx.Err()
}

// ForEver runs an explicit `for { ... }` loop. It never materializes a
// sentinel list: each iteration is bounded by the body, break, or Context
// cancellation. continue starts the next iteration after the body returns.
func ForEver(ctx context.Context, body func(context.Context) Result) (Value, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("for has no body evaluator")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := ResolveValue(ctx, body(ctx)); err != nil {
			if errors.Is(err, forBreak) {
				return nil, nil
			}
			if errors.Is(err, forContinue) {
				continue
			}
			return nil, err
		}
	}
}
