package main

import (
	"context"
	"errors"
	"testing"

	"lipalpha/runtime"
)

func TestSessionJourney(t *testing.T) {
	values, err := demo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := runtime.FormatValue(values)
	want := `[{"fetch_calls":2,"tick":1,"value":{"count":0,"result":3},"writes":1},{"fetch_calls":3,"tick":2,"value":{"count":2,"result":3},"writes":2},{"fetch_calls":3,"tick":3,"value":null,"writes":2}]`
	if got != want {
		t.Fatalf("got %s", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := demo(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
