package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"lipalpha/examples/host_adapter/flow"
	"lipalpha/runtime"
)

// This is a real Host Adapter: LIP sees load_profile, while the adapter owns
// file IO, JSON decoding, cancellation and Go error values.
func main() {
	file, err := os.CreateTemp("", "lip-profile-*.json")
	if err != nil {
		panic(err)
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(`{"name":"LIP"}`); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}

	var calls atomic.Int64
	host := runtime.DefaultHost()
	host.RegisterPure("load_profile", func(ctx context.Context, args []runtime.Value) runtime.Result {
		if len(args) != 1 {
			return runtime.Failed(fmt.Errorf("load_profile expects one path"))
		}
		path, ok := args[0].(string)
		if !ok {
			return runtime.Failed(fmt.Errorf("load_profile expects a string path"))
		}
		calls.Add(1)
		select {
		case <-ctx.Done():
			return runtime.Failed(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return runtime.Failed(err)
		}
		var profile map[string]any
		if err := json.Unmarshal(data, &profile); err != nil {
			return runtime.Failed(err)
		}
		return runtime.Ready(profile)
	})

	started := time.Now()
	value, _, err := hostflow.Run(context.Background(), host, map[string]runtime.Value{"path": file.Name()})
	if err != nil {
		panic(err)
	}
	fmt.Printf("value=%v calls=%d elapsed=%s\n", value, calls.Load(), time.Since(started).Round(time.Millisecond))
}
