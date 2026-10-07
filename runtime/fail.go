package runtime

import (
	"context"
	"fmt"
)

// Fail is the explicit pure failure expression: it never produces a value,
// never calls a Host operation, and respects an already-cancelled context.
func Fail(ctx context.Context, message Value) (Value, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text, ok := message.(string)
	if !ok {
		return nil, fmt.Errorf("fail expects string, got %T", message)
	}
	return nil, fmt.Errorf("fail: %s", text)
}
