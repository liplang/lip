package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"lipalpha/runtime"
)

func main() {
	worker, err := runtime.NewPythonWorker(context.Background(), runtime.PythonWorkerConfig{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start Python worker:", err)
		os.Exit(1)
	}
	defer worker.Close()

	// Every dotted operation in the Flow is resolved by the generic fallback;
	// no per-library registration is needed.
	host := runtime.NewPythonHost(worker)
	data := make([]byte, 5*8)
	for i, value := range []float64{1, 2, 3, 4, 5} {
		binary.LittleEndian.PutUint64(data[i*8:], math.Float64bits(value))
	}
	blob, err := worker.PutBlob(context.Background(), data, runtime.PythonBlobMetadata{
		DType: "<f8",
		Shape: []int64{5},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "write Python data blob:", err)
		os.Exit(1)
	}
	defer worker.ReleaseBlob(context.Background(), blob)

	value, trace, err := Run(context.Background(), host, map[string]runtime.Value{
		"values": blob,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "run Scientific:", err)
		for _, event := range trace {
			fmt.Fprintf(os.Stderr, "%s %s %s\n", event.Node, event.Status, event.Reason)
		}
		os.Exit(1)
	}
	fmt.Printf("Python result: %#v\n", value)
}
