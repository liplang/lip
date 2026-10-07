package runtime

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestPythonWorker(t *testing.T) *PythonWorker {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 is unavailable: %v", err)
	}
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3"})
	if err != nil {
		t.Fatalf("python3 is present but worker startup failed: %v", err)
	}
	t.Cleanup(func() { _ = worker.Close() })
	return worker
}

func TestPythonWorkerRoundTripAndCapabilities(t *testing.T) {
	worker := newTestPythonWorker(t)
	if worker.PythonVersion() == "" {
		t.Fatal("worker did not report a Python version")
	}
	caps, err := worker.PythonCapabilities(context.Background())
	if err != nil || len(caps) == 0 {
		t.Fatalf("capabilities=%v err=%v", caps, err)
	}
	result := worker.Call(context.Background(), "matrix_multiply", []Value{
		[]Value{[]Value{2, 0}, []Value{0, 3}},
		[]Value{[]Value{4, 1}, []Value{2, 5}},
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	got, ok := result.Value.([]any)
	if !ok || len(got) != 2 || got[0].([]any)[0] != float64(8) || got[1].([]any)[1] != float64(15) {
		t.Fatalf("matrix result = %#v", result.Value)
	}
	if result := worker.Call(context.Background(), "sleep", []Value{0}); result.Err != nil || result.Value != nil {
		t.Fatalf("sleep result=%#v err=%v", result.Value, result.Err)
	}
	result = worker.Call(context.Background(), "math.sqrt", []Value{81})
	if result.Err != nil || result.Value != float64(9) {
		t.Fatalf("dynamic import result=%#v err=%v", result.Value, result.Err)
	}
	result = worker.Call(context.Background(), "os.getcwd", nil)
	if result.Err != nil || result.Value == "" {
		t.Fatalf("unrestricted import result=%#v err=%v", result.Value, result.Err)
	}
	result = worker.Call(context.Background(), "fractions.Fraction", []Value{1, 3})
	if result.Err != nil {
		t.Fatalf("object creation error=%v", result.Err)
	}
	handle, ok := result.Value.(map[string]any)
	if !ok || handle["$python_handle"] == nil {
		t.Fatalf("object result=%#v, want Python handle", result.Value)
	}
	result = worker.Call(context.Background(), "python.call", []Value{handle, "limit_denominator", []Value{10}})
	if result.Err != nil {
		t.Fatalf("object method error=%v", result.Err)
	}
	result = worker.Call(context.Background(), "python.to_json", []Value{result.Value})
	if result.Err != nil || result.Value != "1/3" {
		t.Fatalf("object serialization result=%#v err=%v", result.Value, result.Err)
	}
	if result = worker.Call(context.Background(), "python.release", []Value{handle}); result.Err != nil {
		t.Fatalf("object release error=%v", result.Err)
	}
}

func TestPythonWorkerBlobDataPlaneRoundTrip(t *testing.T) {
	worker := newTestPythonWorker(t)
	available := worker.Call(context.Background(), "python.module_available", []Value{"numpy"})
	if available.Err != nil || available.Value != true {
		t.Skip("numpy is unavailable")
	}
	data := make([]byte, 6*8)
	for i := 0; i < 6; i++ {
		binary.LittleEndian.PutUint64(data[i*8:], uint64(i+1))
	}
	blob, err := worker.PutBlob(context.Background(), data, PythonBlobMetadata{
		DType: "<i8",
		Shape: []int64{2, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := worker.Call(context.Background(), "numpy.sum", []Value{blob})
	if result.Err != nil || result.Value != float64(21) {
		t.Fatalf("mapped numpy sum=%#v err=%v", result.Value, result.Err)
	}
	result = worker.Call(context.Background(), "numpy.shape", []Value{blob})
	if result.Err != nil || !reflect.DeepEqual(result.Value, []any{float64(2), float64(3)}) {
		t.Fatalf("mapped numpy shape=%#v err=%v", result.Value, result.Err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if result = worker.Call(ctx, "sleep", []Value{0.2}); !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("worker cancellation for blob restart=%v", result.Err)
	}
	result = worker.Call(context.Background(), "numpy.sum", []Value{blob})
	if result.Err != nil || result.Value != float64(21) {
		t.Fatalf("mapped numpy sum after worker restart=%#v err=%v", result.Value, result.Err)
	}
	if err := worker.ReleaseBlob(context.Background(), blob); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(worker.dataDir, blob.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released blob still exists: %v", err)
	}
}

func TestPythonWorkerPutFileUsesAtomicBlobPublish(t *testing.T) {
	worker := newTestPythonWorker(t)
	source := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(source, []byte("stable payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	blob, err := worker.PutFile(context.Background(), source, PythonBlobMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if blob.Size != int64(len("stable payload")) || blob.SHA256 == "" {
		t.Fatalf("blob metadata=%#v", blob)
	}
	if _, err := os.Stat(filepath.Join(worker.dataDir, blob.Name)); err != nil {
		t.Fatalf("published blob missing: %v", err)
	}
	if err := worker.ReleaseBlob(context.Background(), blob); err != nil {
		t.Fatal(err)
	}
}

func TestPythonWorkerNumpyBlobIsReadOnlyAndReleasable(t *testing.T) {
	worker := newTestPythonWorker(t)
	available := worker.Call(context.Background(), "python.module_available", []Value{"numpy"})
	if available.Err != nil || available.Value != true {
		t.Skip("numpy is unavailable")
	}
	source := filepath.Join(t.TempDir(), "values.npy")
	content := makeNumpyV1Int64([]int64{1, 2, 3, 4, 5, 6})
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	blob, err := worker.PutFile(context.Background(), source, PythonBlobMetadata{Format: "npy"})
	if err != nil {
		t.Fatal(err)
	}
	result := worker.Call(context.Background(), "numpy.sum", []Value{blob})
	if result.Err != nil || result.Value != float64(21) {
		t.Fatalf("npy sum=%#v err=%v", result.Value, result.Err)
	}
	opened := worker.Call(context.Background(), "python.open_blob", []Value{blob})
	if opened.Err != nil {
		t.Fatalf("open npy blob=%v", opened.Err)
	}
	if result := worker.Call(context.Background(), "python.call", []Value{opened.Value, "__setitem__", []Value{0, 99}}); result.Err == nil {
		t.Fatal("expected a read-only npy mapping")
	}
	if err := worker.ReleaseBlob(context.Background(), blob); err != nil {
		t.Fatal(err)
	}
	if result := worker.Call(context.Background(), "python.call", []Value{opened.Value, "tolist", []Value{}}); result.Err == nil {
		t.Fatal("expected a released blob handle to be invalid")
	}
}

func TestPythonWorkerBlobDigestRejectsTampering(t *testing.T) {
	worker := newTestPythonWorker(t)
	blob, err := worker.PutBlob(context.Background(), []byte("original"), PythonBlobMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worker.dataDir, blob.Name), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := worker.Call(context.Background(), "echo", []Value{blob})
	var pythonErr *PythonError
	if !errors.As(result.Err, &pythonErr) || pythonErr.Type != "ValueError" {
		t.Fatalf("tampered blob error=%v", result.Err)
	}
	if err := worker.ReleaseBlob(context.Background(), blob); err == nil {
		t.Fatal("expected release to report tampered blob")
	}
	if _, err := os.Stat(filepath.Join(worker.dataDir, blob.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tampered blob was not cleaned up: %v", err)
	}
}

func TestPythonWorkerOwnDataDirIsRemovedOnClose(t *testing.T) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3"})
	if err != nil {
		t.Skipf("python3 is unavailable: %v", err)
	}
	dir := worker.dataDir
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned data directory still exists: %v", err)
	}
}

func TestPythonWorkerBlobLimitLeavesNoPartialFile(t *testing.T) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3", MaxBlobBytes: 4})
	if err != nil {
		t.Skipf("python3 is unavailable: %v", err)
	}
	defer worker.Close()
	if _, err := worker.PutBlob(context.Background(), []byte("12345"), PythonBlobMetadata{}); err == nil {
		t.Fatal("expected blob size limit error")
	}
	entries, err := os.ReadDir(worker.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed blob left data files: %v", entries)
	}
}

func TestPythonWorkerDataAndHandleBudgets(t *testing.T) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{
		Python:           "python3",
		MaxBlobBytes:     10,
		MaxDataBytes:     5,
		MaxOpenBlobs:     1,
		MaxPythonHandles: 1,
	})
	if err != nil {
		t.Skipf("python3 is unavailable: %v", err)
	}
	defer worker.Close()
	first, err := worker.PutBlob(context.Background(), []byte("1234"), PythonBlobMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.PutBlob(context.Background(), []byte("56"), PythonBlobMetadata{}); err == nil {
		t.Fatal("expected total data budget error")
	}
	if result := worker.Call(context.Background(), "echo", []Value{first}); result.Err != nil {
		t.Fatalf("first blob open=%v", result.Err)
	}
	second, err := worker.PutBlob(context.Background(), []byte("5"), PythonBlobMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if result := worker.Call(context.Background(), "echo", []Value{second}); result.Err == nil {
		t.Fatal("expected open blob budget error")
	}
	if err := worker.ReleaseBlob(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := worker.ReleaseBlob(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	firstHandle := worker.Call(context.Background(), "fractions.Fraction", []Value{1, 3})
	if firstHandle.Err != nil {
		t.Fatal(firstHandle.Err)
	}
	if result := worker.Call(context.Background(), "fractions.Fraction", []Value{1, 4}); result.Err == nil {
		t.Fatal("expected Python handle budget error")
	}
	if result := worker.Call(context.Background(), "python.release", []Value{firstHandle.Value}); result.Err != nil {
		t.Fatal(result.Err)
	}
}

func makeNumpyV1Int64(values []int64) []byte {
	header := "{'descr': '<i8', 'fortran_order': False, 'shape': (2, 3), }"
	padding := 16 - ((10 + len(header) + 1) % 16)
	header += strings.Repeat(" ", padding-1) + "\n"
	result := make([]byte, 10+len(header)+len(values)*8)
	copy(result, []byte("\x93NUMPY\x01\x00"))
	binary.LittleEndian.PutUint16(result[8:10], uint16(len(header)))
	copy(result[10:], []byte(header))
	for i, value := range values {
		binary.LittleEndian.PutUint64(result[10+len(header)+i*8:], uint64(value))
	}
	return result
}

func TestPythonWorkerModulePolicyCanBeRestricted(t *testing.T) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{
		Python:         "python3",
		AllowedModules: []string{"math"},
	})
	if err != nil {
		t.Skipf("python3 is unavailable: %v", err)
	}
	defer worker.Close()
	result := worker.Call(context.Background(), "statistics.mean", []Value{[]Value{1, 2, 3}})
	var pythonErr *PythonError
	if !errors.As(result.Err, &pythonErr) || pythonErr.Type != "PermissionError" {
		t.Fatalf("policy error=%v", result.Err)
	}
	result = worker.Call(context.Background(), "os.system", []Value{"echo should-not-run"})
	if !errors.As(result.Err, &pythonErr) || pythonErr.Type != "PermissionError" {
		t.Fatalf("deny policy error=%v", result.Err)
	}
}

func TestPythonWorkerControlOperationsRemainAvailableWithAllowlist(t *testing.T) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{
		Python:         "python3",
		AllowedModules: []string{"fractions"},
	})
	if err != nil {
		t.Skipf("python3 is unavailable: %v", err)
	}
	defer worker.Close()

	result := worker.Call(context.Background(), "fractions.Fraction", []Value{1, 3})
	if result.Err != nil {
		t.Fatalf("object creation error=%v", result.Err)
	}
	handle, ok := result.Value.(map[string]any)
	if !ok {
		t.Fatalf("object result=%#v", result.Value)
	}
	result = worker.Call(context.Background(), "python.call", []Value{handle, "limit_denominator", []Value{10}})
	if result.Err != nil || result.Value == nil {
		t.Fatalf("python.call under allowlist: value=%#v err=%v", result.Value, result.Err)
	}
	result = worker.Call(context.Background(), "python.to_json", []Value{result.Value})
	if result.Err != nil || result.Value != "1/3" {
		t.Fatalf("python.to_json under allowlist: value=%#v err=%v", result.Value, result.Err)
	}
	result = worker.Call(context.Background(), "python.module_available", []Value{"fractions"})
	if result.Err != nil || result.Value != true {
		t.Fatalf("python.module_available under allowlist: value=%#v err=%v", result.Value, result.Err)
	}
	if result = worker.Call(context.Background(), "python.release", []Value{handle}); result.Err != nil {
		t.Fatalf("python.release under allowlist: %v", result.Err)
	}
}

func TestPythonWorkerErrorAndRestartAfterCancellation(t *testing.T) {
	worker := newTestPythonWorker(t)
	result := worker.Call(context.Background(), "fail", []Value{"bad input"})
	var pythonErr *PythonError
	if !errors.As(result.Err, &pythonErr) || pythonErr.Type != "RuntimeError" || !strings.Contains(pythonErr.Error(), "bad input") {
		t.Fatalf("error=%v, pythonErr=%#v", result.Err, pythonErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result = worker.Call(ctx, "sleep", []Value{0.5})
	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("cancel error=%v", result.Err)
	}
	result = worker.Call(context.Background(), "echo", []Value{"restarted"})
	if result.Err != nil || result.Value != "restarted" {
		t.Fatalf("restart result=%#v err=%v", result.Value, result.Err)
	}
}

func TestPythonWorkerRegistersOnHostAndRunsInGraph(t *testing.T) {
	worker := newTestPythonWorker(t)
	host := NewHost()
	host.RegisterPythonPure("python_sum", worker, "sum")
	host.RegisterPythonReadOnly("python_mean", worker, "mean")
	graph := NewGraph()
	graph.Add(NodeSpec{Name: "sum", Op: "python_sum", Effect: EffectPure, Output: true, Eval: func(ctx context.Context, values map[string]Value) Result {
		return host.Call(ctx, "python_sum", []Value{values["numbers"]})
	}})
	value, _, err := graph.RunAuto(context.Background(), host, map[string]Value{"numbers": []int{1, 2, 3, 4}})
	if err != nil || value != float64(10) {
		t.Fatalf("value=%#v err=%v", value, err)
	}
}

func TestPythonHostDottedFallback(t *testing.T) {
	worker := newTestPythonWorker(t)
	host := NewPythonHost(worker)
	result := host.Call(context.Background(), "math.sqrt", []Value{144})
	if result.Err != nil || result.Value != float64(12) {
		t.Fatalf("value=%#v err=%v", result.Value, result.Err)
	}
	if host.EffectOf("torch.nn.functional.relu") != EffectExternalWrite {
		t.Fatalf("dynamic Python fallback effect=%s", host.EffectOf("torch.nn.functional.relu"))
	}
}

func TestPythonWorkerQueueIsBounded(t *testing.T) {
	worker, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3", MaxQueue: 1})
	if err != nil {
		t.Skipf("python3 is unavailable: %v", err)
	}
	defer worker.Close()

	started := make(chan struct{})
	var once sync.Once
	ctx := context.Background()
	go func() {
		once.Do(func() { close(started) })
		_ = worker.Call(ctx, "sleep", []Value{0.08})
	}()
	<-started
	time.Sleep(5 * time.Millisecond)
	short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result := worker.Call(short, "echo", []Value{"queued"})
	if result.Err == nil {
		t.Fatal("expected bounded queue call to time out")
	}
}

func TestPythonWorkerRejectsBrokenHandshake(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "broken.py")
	if err := os.WriteFile(script, []byte("print('{\\\"type\\\":\\\"ready\\\",\\\"protocol\\\":99}', flush=True)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewPythonWorker(context.Background(), PythonWorkerConfig{Python: "python3", Script: script})
	if err == nil || !strings.Contains(err.Error(), "protocol mismatch") {
		t.Fatalf("err=%v, want protocol mismatch", err)
	}
}

func TestPythonWorkerStaleHandlesFailAfterRestart(t *testing.T) {
	worker := newTestPythonWorker(t)
	ctx := context.Background()
	old := worker.Call(ctx, "fractions.Fraction", []Value{1, 3})
	if old.Err != nil {
		t.Fatal(old.Err)
	}
	if result := worker.Call(ctx, "os._exit", []Value{2}); result.Err == nil {
		t.Fatal("worker crash became a success")
	}
	fresh := worker.Call(ctx, "fractions.Fraction", []Value{1, 4})
	if fresh.Err != nil {
		t.Fatal(fresh.Err)
	}
	if result := worker.Call(ctx, "python.to_json", []Value{old.Value}); result.Err == nil {
		t.Fatal("stale handle resolved to a new object after restart")
	}
	if result := worker.Call(ctx, "python.to_json", []Value{fresh.Value}); result.Err != nil || result.Value != "1/4" {
		t.Fatalf("fresh=%v err=%v", result.Value, result.Err)
	}
	if result := worker.Call(ctx, "python.release", []Value{fresh.Value}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := worker.Call(ctx, "python.to_json", []Value{fresh.Value}); result.Err == nil {
		t.Fatal("released handle remained usable")
	}
	if result := worker.Call(ctx, "python.release", []Value{fresh.Value}); result.Err == nil {
		t.Fatal("double release was accepted")
	}
}

func TestPythonWorkerCachedBlobRejectsTampering(t *testing.T) {
	worker := newTestPythonWorker(t)
	blob, err := worker.PutBlob(context.Background(), []byte("1234"), PythonBlobMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if result := worker.Call(context.Background(), "echo", []Value{blob}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if err := os.WriteFile(filepath.Join(worker.dataDir, blob.Name), []byte("4321"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := worker.Call(context.Background(), "echo", []Value{blob}); result.Err == nil {
		t.Fatal("cached blob accepted changed bytes")
	}
}
