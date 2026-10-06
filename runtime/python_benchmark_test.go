package runtime

import (
	"context"
	"encoding/binary"
	"testing"
)

// These benchmarks make cold-start, resident-worker, JSON and data-plane
// behavior measurable without making latency claims in the documentation.
func BenchmarkPythonWorkerWarmCall(b *testing.B) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3"})
	if err != nil {
		b.Skipf("python3 is unavailable: %v", err)
	}
	defer worker.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := worker.Call(context.Background(), "echo", []Value{i})
		if result.Err != nil {
			b.Fatal(result.Err)
		}
	}
}

func BenchmarkPythonWorkerColdStart(b *testing.B) {
	for i := 0; i < b.N; i++ {
		worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3"})
		if err != nil {
			b.Skipf("python3 is unavailable: %v", err)
		}
		_ = worker.Close()
	}
}

func BenchmarkPythonWorkerJSONArrayCall(b *testing.B) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3"})
	if err != nil {
		b.Skipf("python3 is unavailable: %v", err)
	}
	defer worker.Close()
	if result := worker.Call(context.Background(), "python.module_available", []Value{"numpy"}); result.Err != nil || result.Value != true {
		b.Skip("numpy is unavailable")
	}
	values := make([]Value, 64*1024)
	for i := range values {
		values[i] = i
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := worker.Call(context.Background(), "numpy.sum", []Value{values})
		if result.Err != nil {
			b.Fatal(result.Err)
		}
	}
}

func BenchmarkPythonWorkerMappedArrayCall(b *testing.B) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3"})
	if err != nil {
		b.Skipf("python3 is unavailable: %v", err)
	}
	defer worker.Close()
	if result := worker.Call(context.Background(), "python.module_available", []Value{"numpy"}); result.Err != nil || result.Value != true {
		b.Skip("numpy is unavailable")
	}
	data := make([]byte, 64*1024*8)
	for i := 0; i < 64*1024; i++ {
		binary.LittleEndian.PutUint64(data[i*8:], uint64(i))
	}
	blob, err := worker.PutBlob(context.Background(), data, PythonBlobMetadata{DType: "<i8", Shape: []int64{64 * 1024}})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = worker.ReleaseBlob(context.Background(), blob) }()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := worker.Call(context.Background(), "numpy.sum", []Value{blob})
		if result.Err != nil {
			b.Fatal(result.Err)
		}
	}
}
