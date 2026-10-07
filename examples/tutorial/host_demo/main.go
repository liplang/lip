package main

import (
	"context"
	"fmt"
	"os"

	"lipalpha/examples/tutorial/sessionflow"
	"lipalpha/runtime"
)

// demo owns each adapter and instance. The read adapter deliberately fails once
// to demonstrate Retry, then returns an already-resolved asynchronous Result.
func demo(ctx context.Context) ([]runtime.Value, error) {
	host := runtime.DefaultHost()
	fetchCalls, writes := 0, 0
	host.RegisterReadOnly("fetch", func(ctx context.Context, args []runtime.Value) runtime.Result {
		if err := ctx.Err(); err != nil {
			return runtime.Failed(err)
		}
		if len(args) != 1 {
			return runtime.Failed(fmt.Errorf("fetch expects one number"))
		}
		if err := runtime.CheckType(args[0], "number"); err != nil {
			return runtime.Failed(err)
		}
		fetchCalls++
		if fetchCalls == 1 {
			return runtime.Failed(fmt.Errorf("temporary read failure"))
		}
		future := make(chan runtime.Result, 1)
		future <- runtime.Ready(args[0])
		close(future)
		return runtime.Await(future)
	})
	host.Register("write_report", func(ctx context.Context, args []runtime.Value) runtime.Result {
		if err := ctx.Err(); err != nil {
			return runtime.Failed(err)
		}
		if len(args) != 1 {
			return runtime.Failed(fmt.Errorf("write_report expects one object"))
		}
		if err := runtime.CheckType(args[0], "object"); err != nil {
			return runtime.Failed(err)
		}
		writes++
		return runtime.Ready(nil)
	})
	instance, err := sessionflow.NewInstance(host, map[string]runtime.Value{"input": 1, "enabled": true})
	if err != nil {
		return nil, err
	}
	results := []runtime.Value{}
	tick := func(updates map[string]runtime.Value) error {
		value, _, err := instance.Tick(ctx, updates)
		if err != nil {
			return err
		}
		results = append(results, map[string]runtime.Value{"tick": instance.TickCount(), "value": value, "fetch_calls": fetchCalls, "writes": writes})
		return nil
	}
	if err := tick(nil); err != nil {
		return nil, err
	}
	if err := instance.SetState("count", 2); err != nil {
		return nil, err
	}
	if err := tick(nil); err != nil {
		return nil, err
	}
	if err := tick(map[string]runtime.Value{"enabled": false}); err != nil {
		return nil, err
	}
	return results, nil
}

func main() {
	values, err := demo(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	text, err := runtime.FormatValue(values)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(text)
}
