package runtime

// This file contains the first Python adapter for LIP.  The adapter is
// deliberately a process boundary: the compiler and scheduler do not depend
// on Python, and a broken interpreter can be replaced without taking down the
// Go process.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const pythonProtocolVersion = 1

// PythonWorkerConfig controls the child process used by NewPythonWorker.
// When Script is empty, the worker bundled with the runtime is used.  A
// custom script is useful for testing or for deploying a compatible worker
// with extra operations.
type PythonWorkerConfig struct {
	// Python is the interpreter executable. It defaults to python3, then
	// python when python3 is not available.
	Python string
	// Script is a compatible JSONL worker script. Empty means the bundled
	// generic worker.
	Script string
	// Args are passed to the worker after the script (or -c program).
	Args []string
	Env  []string
	Dir  string

	// AllowedModules restricts dotted imports by top-level module or prefix.
	// An empty list means any installed module is usable except DeniedModules.
	// For example, "torch" enables torch and its submodules, while
	// "transformers.*" enables only that prefix when an allowlist is set.
	AllowedModules []string
	// DeniedModules blocks dangerous module roots. Nil uses the conservative
	// no-deny default; set it explicitly for a restricted deployment.
	DeniedModules []string
	// AllowAnyModule disables both module lists. Use only in an already
	// isolated environment; it permits arbitrary installed Python modules.
	AllowAnyModule bool

	// MaxQueue bounds accepted calls, including the call currently being
	// processed. A value <= 0 uses 16.
	MaxQueue int
	// StartupTimeout bounds the initial ready handshake. A value <= 0 uses
	// five seconds.
	StartupTimeout time.Duration
	// MaxMessageBytes rejects an individual protocol line larger than this
	// value. A value <= 0 uses 16 MiB.
	MaxMessageBytes int
	// Stderr receives worker diagnostics. It defaults to io.Discard so that
	// stdout remains exclusively the protocol stream.
	Stderr io.Writer

	// DataDir is the local directory used by the P2 blob data plane. Empty
	// creates a private temporary directory that is removed when the worker
	// closes. A caller-owned directory is never removed automatically.
	DataDir string
	// MaxBlobBytes bounds one blob written by PutBlob or PutFile. A value <= 0
	// uses 512 MiB. The control protocol still carries only a small descriptor.
	MaxBlobBytes int64
	// MaxDataBytes bounds the total size of blobs created by this worker. A
	// value <= 0 uses 2 GiB. Caller-owned DataDir files from older workers are
	// not counted; release them explicitly or use a private DataDir.
	MaxDataBytes int64
	// MaxOpenBlobs bounds mappings cached by the bundled Python worker. A value
	// <= 0 uses 64.
	MaxOpenBlobs int
	// MaxPythonHandles bounds non-JSON objects retained in a worker session. A
	// value <= 0 uses 10000.
	MaxPythonHandles int
}

func (c PythonWorkerConfig) withDefaults() PythonWorkerConfig {
	if c.MaxQueue <= 0 {
		c.MaxQueue = 16
	}
	if c.StartupTimeout <= 0 {
		c.StartupTimeout = 5 * time.Second
	}
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = 16 << 20
	}
	if c.Stderr == nil {
		c.Stderr = io.Discard
	}
	if c.MaxBlobBytes <= 0 {
		c.MaxBlobBytes = 512 << 20
	}
	if c.MaxDataBytes <= 0 {
		c.MaxDataBytes = 2 << 30
	}
	if c.MaxOpenBlobs <= 0 {
		c.MaxOpenBlobs = 64
	}
	if c.MaxPythonHandles <= 0 {
		c.MaxPythonHandles = 10000
	}
	return c
}

// PythonBlob describes an immutable local data-plane object. The descriptor
// is safe to place in a normal runtime.Value: only metadata crosses JSONL,
// while the bytes stay in DataDir. A blob is readable by the matching worker
// process and is released explicitly with PythonWorker.ReleaseBlob.
type PythonBlob struct {
	Name   string  `json:"$lip_blob"`
	Size   int64   `json:"size"`
	SHA256 string  `json:"sha256"`
	Format string  `json:"format,omitempty"`
	DType  string  `json:"dtype,omitempty"`
	Shape  []int64 `json:"shape,omitempty"`
	Order  string  `json:"order,omitempty"`
}

// PythonBlobMetadata describes the bytes supplied to PutBlob or PutFile.
// Format "raw" is a binary buffer, optionally typed for NumPy. Format "npy"
// is a NumPy .npy file loaded read-only by the Python worker.
type PythonBlobMetadata struct {
	Format string
	DType  string
	Shape  []int64
	Order  string
}

// PythonWorker is a long-lived, one-request-at-a-time Python process. Calls
// are queued up to MaxQueue and share the process, so imported modules and
// warmed models remain resident between LIP nodes.
type PythonWorker struct {
	config PythonWorkerConfig

	callSlot      chan struct{}
	stateMu       sync.Mutex
	proc          *pythonProcess
	closed        bool
	queue         chan struct{}
	nextID        atomic.Uint64
	pythonVersion string
	capabilities  []string
	dataDir       string
	dataDirOwned  bool
	dataMu        sync.Mutex
	nextBlobID    atomic.Uint64
	blobs         map[string]int64
	dataBytes     int64
}

type pythonProcess struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	python       string
	capabilities []string
}

type pythonReady struct {
	Type         string   `json:"type"`
	Protocol     int      `json:"protocol"`
	Python       string   `json:"python"`
	Capabilities []string `json:"capabilities"`
}

type pythonRequest struct {
	Type     string  `json:"type"`
	ID       string  `json:"id"`
	Op       string  `json:"op"`
	Args     []Value `json:"args"`
	Session  string  `json:"session,omitempty"`
	Deadline string  `json:"deadline,omitempty"`
}

type pythonResponse struct {
	Type  string          `json:"type,omitempty"`
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value"`
	Error *pythonError    `json:"error,omitempty"`
}

type pythonError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// PythonError preserves the exception class reported by a worker while still
// being easy to inspect with errors.As.
type PythonError struct {
	Operation string
	RequestID string
	Type      string
	Message   string
}

func (e *PythonError) Error() string {
	if e == nil {
		return "python error"
	}
	return fmt.Sprintf("python operation %q (request %s) failed (%s): %s", e.Operation, e.RequestID, e.Type, e.Message)
}

// NewPythonWorker starts a resident worker and waits for its protocol
// handshake. Python is only needed when this constructor is called; importing
// runtime and compiling a LIP program remains possible without Python.
func NewPythonWorker(ctx context.Context, config PythonWorkerConfig) (*PythonWorker, error) {
	if ctx == nil {
		return nil, errNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config = config.withDefaults()
	dataDir, owned, err := preparePythonDataDir(config.DataDir)
	if err != nil {
		return nil, err
	}
	config.DataDir = dataDir
	w := &PythonWorker{config: config, queue: make(chan struct{}, config.MaxQueue), callSlot: make(chan struct{}, 1), dataDir: dataDir, dataDirOwned: owned, blobs: make(map[string]int64)}
	proc, err := w.start(ctx)
	if err != nil {
		if owned {
			_ = os.RemoveAll(dataDir)
		}
		return nil, err
	}
	w.proc = proc
	w.pythonVersion = proc.python
	w.capabilities = append([]string(nil), proc.capabilities...)
	return w, nil
}

func (w *PythonWorker) start(ctx context.Context) (*pythonProcess, error) {
	python := w.config.Python
	if python == "" {
		python = "python3"
		if _, err := exec.LookPath(python); err != nil {
			python = "python"
		}
	}

	args := make([]string, 0, len(w.config.Args)+4)
	args = append(args, "-u")
	if w.config.Script != "" {
		args = append(args, w.config.Script)
	} else {
		args = append(args, "-c", bundledPythonWorker)
	}
	args = append(args, w.config.Args...)
	cmd := exec.Command(python, args...)
	cmd.Dir = w.config.Dir
	cmd.Env = append(os.Environ(), w.config.Env...)
	cmd.Env = append(cmd.Env,
		"LIP_PYTHON_ALLOWED_MODULES="+strings.Join(w.config.AllowedModules, ","),
		"LIP_PYTHON_DENIED_MODULES="+strings.Join(w.config.DeniedModules, ","),
		"LIP_PYTHON_ALLOW_ANY="+strconv.FormatBool(w.config.AllowAnyModule),
		"LIP_PYTHON_DATA_DIR="+w.dataDir,
		"LIP_PYTHON_MAX_BLOB_BYTES="+strconv.FormatInt(w.config.MaxBlobBytes, 10),
		"LIP_PYTHON_MAX_OPEN_BLOBS="+strconv.Itoa(w.config.MaxOpenBlobs),
		"LIP_PYTHON_MAX_HANDLES="+strconv.Itoa(w.config.MaxPythonHandles),
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("python worker stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("python worker stdin: %w", err)
	}
	cmd.Stderr = w.config.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start python worker %q: %w", python, err)
	}
	proc := &pythonProcess{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 32*1024)}
	line, err := readLineContext(ctx, proc.stdout, w.config.StartupTimeout, w.config.MaxMessageBytes)
	if err != nil {
		killPythonProcess(proc)
		return nil, fmt.Errorf("python worker handshake: %w", err)
	}
	var ready pythonReady
	if err := json.Unmarshal(line, &ready); err != nil {
		killPythonProcess(proc)
		return nil, fmt.Errorf("python worker handshake is invalid JSON: %w", err)
	}
	if ready.Type != "ready" || ready.Protocol != pythonProtocolVersion {
		killPythonProcess(proc)
		return nil, fmt.Errorf("python worker protocol mismatch: got type=%q protocol=%d, want ready/%d", ready.Type, ready.Protocol, pythonProtocolVersion)
	}
	proc.python = ready.Python
	proc.capabilities = append([]string(nil), ready.Capabilities...)
	return proc, nil
}

func preparePythonDataDir(configured string) (string, bool, error) {
	if configured == "" {
		dir, err := os.MkdirTemp("", "lip-python-data-")
		if err != nil {
			return "", false, fmt.Errorf("create python data directory: %w", err)
		}
		return dir, true, nil
	}
	dir, err := filepath.Abs(configured)
	if err != nil {
		return "", false, fmt.Errorf("resolve python data directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, fmt.Errorf("create python data directory %q: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", false, fmt.Errorf("stat python data directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("python data path %q is not a directory", dir)
	}
	return dir, false, nil
}

func (m PythonBlobMetadata) normalized() (PythonBlobMetadata, error) {
	if m.Format == "" {
		m.Format = "raw"
	}
	if m.Format != "raw" && m.Format != "npy" {
		return PythonBlobMetadata{}, fmt.Errorf("unsupported Python blob format %q", m.Format)
	}
	if m.Order == "" {
		m.Order = "C"
	}
	if m.Order != "C" && m.Order != "F" {
		return PythonBlobMetadata{}, fmt.Errorf("Python blob order must be C or F, got %q", m.Order)
	}
	for _, dimension := range m.Shape {
		if dimension < 0 {
			return PythonBlobMetadata{}, fmt.Errorf("Python blob shape contains negative dimension %d", dimension)
		}
	}
	if m.Format == "npy" && (m.DType != "" || len(m.Shape) != 0) {
		return PythonBlobMetadata{}, fmt.Errorf("npy blobs do not accept raw dtype or shape metadata")
	}
	if m.Format == "raw" && ((m.DType == "") != (len(m.Shape) == 0)) {
		return PythonBlobMetadata{}, fmt.Errorf("raw typed blobs require both dtype and shape")
	}
	return m, nil
}

func (w *PythonWorker) checkBlobContext(ctx context.Context) error {
	if w == nil {
		return errors.New("nil python worker")
	}
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.stateMu.Lock()
	closed := w.closed
	w.stateMu.Unlock()
	if closed {
		return errors.New("python worker is closed")
	}
	return nil
}

// PutBlob atomically writes bytes into the worker's local data directory and
// returns a small descriptor suitable for a runtime.Value. The worker verifies
// size and SHA-256 before it maps the file, so a partially written blob is
// never visible to Python.
func (w *PythonWorker) PutBlob(ctx context.Context, data []byte, metadata PythonBlobMetadata) (PythonBlob, error) {
	if err := w.checkBlobContext(ctx); err != nil {
		return PythonBlob{}, err
	}
	metadata, err := metadata.normalized()
	if err != nil {
		return PythonBlob{}, err
	}
	if int64(len(data)) > w.config.MaxBlobBytes {
		return PythonBlob{}, fmt.Errorf("Python blob is %d bytes, exceeds limit %d", len(data), w.config.MaxBlobBytes)
	}
	w.dataMu.Lock()
	defer w.dataMu.Unlock()
	if err := w.checkBlobContext(ctx); err != nil {
		return PythonBlob{}, err
	}
	if err := w.checkDataBudget(int64(len(data))); err != nil {
		return PythonBlob{}, err
	}
	name := w.newBlobName()
	tmp, err := os.CreateTemp(w.dataDir, ".lip-blob-*")
	if err != nil {
		return PythonBlob{}, fmt.Errorf("create temporary Python blob: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return PythonBlob{}, fmt.Errorf("protect temporary Python blob: %w", err)
	}
	digest := sha256.New()
	if err := writeContext(ctx, io.MultiWriter(tmp, digest), data); err != nil {
		return PythonBlob{}, err
	}
	if err := tmp.Sync(); err != nil {
		return PythonBlob{}, fmt.Errorf("sync Python blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return PythonBlob{}, fmt.Errorf("close temporary Python blob: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(w.dataDir, name)); err != nil {
		return PythonBlob{}, fmt.Errorf("publish Python blob: %w", err)
	}
	syncPythonDataDir(w.dataDir)
	w.blobs[name] = int64(len(data))
	w.dataBytes += int64(len(data))
	ok = true
	return PythonBlob{Name: name, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", digest.Sum(nil)), Format: metadata.Format, DType: metadata.DType, Shape: append([]int64(nil), metadata.Shape...), Order: metadata.Order}, nil
}

// PutFile copies a file into the worker data directory using the same atomic
// publish and digest rules as PutBlob. It is the preferred API when the caller
// already has a large file and wants to avoid one extra in-memory copy.
func (w *PythonWorker) PutFile(ctx context.Context, source string, metadata PythonBlobMetadata) (PythonBlob, error) {
	if err := w.checkBlobContext(ctx); err != nil {
		return PythonBlob{}, err
	}
	metadata, err := metadata.normalized()
	if err != nil {
		return PythonBlob{}, err
	}
	input, err := os.Open(source)
	if err != nil {
		return PythonBlob{}, fmt.Errorf("open Python blob source: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return PythonBlob{}, fmt.Errorf("stat Python blob source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return PythonBlob{}, fmt.Errorf("Python blob source %q is not a regular file", source)
	}
	if info.Size() > w.config.MaxBlobBytes {
		return PythonBlob{}, fmt.Errorf("Python blob is %d bytes, exceeds limit %d", info.Size(), w.config.MaxBlobBytes)
	}
	w.dataMu.Lock()
	defer w.dataMu.Unlock()
	if err := w.checkBlobContext(ctx); err != nil {
		return PythonBlob{}, err
	}
	if err := w.checkDataBudget(info.Size()); err != nil {
		return PythonBlob{}, err
	}
	name := w.newBlobName()
	tmp, err := os.CreateTemp(w.dataDir, ".lip-blob-*")
	if err != nil {
		return PythonBlob{}, fmt.Errorf("create temporary Python blob: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return PythonBlob{}, fmt.Errorf("protect temporary Python blob: %w", err)
	}
	digest := sha256.New()
	written, err := copyContext(ctx, tmp, io.TeeReader(input, digest), w.config.MaxBlobBytes)
	if err != nil {
		return PythonBlob{}, err
	}
	if written != info.Size() {
		return PythonBlob{}, fmt.Errorf("Python blob source changed while reading: got %d bytes, want %d", written, info.Size())
	}
	if err := tmp.Sync(); err != nil {
		return PythonBlob{}, fmt.Errorf("sync Python blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return PythonBlob{}, fmt.Errorf("close temporary Python blob: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(w.dataDir, name)); err != nil {
		return PythonBlob{}, fmt.Errorf("publish Python blob: %w", err)
	}
	syncPythonDataDir(w.dataDir)
	w.blobs[name] = written
	w.dataBytes += written
	ok = true
	return PythonBlob{Name: name, Size: written, SHA256: fmt.Sprintf("%x", digest.Sum(nil)), Format: metadata.Format, DType: metadata.DType, Shape: append([]int64(nil), metadata.Shape...), Order: metadata.Order}, nil
}

func (w *PythonWorker) newBlobName() string {
	return fmt.Sprintf("blob-%d-%d.bin", os.Getpid(), w.nextBlobID.Add(1))
}

func (w *PythonWorker) checkDataBudget(size int64) error {
	if size < 0 {
		return fmt.Errorf("negative Python blob size %d", size)
	}
	if w.config.MaxDataBytes > 0 && w.dataBytes > w.config.MaxDataBytes-size {
		return fmt.Errorf("Python data directory budget exceeded: %d + %d > %d", w.dataBytes, size, w.config.MaxDataBytes)
	}
	return nil
}

func writeContext(ctx context.Context, dst io.Writer, data []byte) error {
	const chunk = 1 << 20
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := len(data)
		if n > chunk {
			n = chunk
		}
		written, err := dst.Write(data[:n])
		if err != nil {
			return fmt.Errorf("write Python blob: %w", err)
		}
		if written != n {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	var total int64
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if total+int64(n) > limit {
				return total, fmt.Errorf("Python blob exceeds limit %d", limit)
			}
			written, writeErr := dst.Write(buf[:n])
			if writeErr != nil {
				return total, fmt.Errorf("write Python blob: %w", writeErr)
			}
			if written != n {
				return total, io.ErrShortWrite
			}
			total += int64(n)
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, fmt.Errorf("read Python blob source: %w", readErr)
		}
	}
}

func syncPythonDataDir(dir string) {
	file, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = file.Sync()
	_ = file.Close()
}

// ReleaseBlob tells Python to close any mapping and remove the published
// file. If the worker already crashed, the local file is still removed so a
// failed Python process cannot leak data-plane storage forever.
func (w *PythonWorker) ReleaseBlob(ctx context.Context, blob PythonBlob) error {
	if w == nil {
		return errors.New("nil python worker")
	}
	if err := blob.validate(w.config.MaxBlobBytes); err != nil {
		return err
	}
	result := w.Call(ctx, "python.release_blob", []Value{blob})
	if result.Err != nil {
		w.removeBlobFile(blob)
		return result.Err
	}
	w.removeBlobFile(blob)
	return nil
}

func (b PythonBlob) validate(maxBytes int64) error {
	if b.Name == "" || filepath.Base(b.Name) != b.Name || b.Name == "." || b.Name == ".." {
		return fmt.Errorf("invalid Python blob name %q", b.Name)
	}
	if b.Size < 0 || (maxBytes > 0 && b.Size > maxBytes) {
		return fmt.Errorf("invalid Python blob size %d", b.Size)
	}
	if len(b.SHA256) != sha256.Size*2 {
		return fmt.Errorf("invalid Python blob SHA-256")
	}
	if _, err := hex.DecodeString(b.SHA256); err != nil {
		return fmt.Errorf("invalid Python blob SHA-256")
	}
	metadata := PythonBlobMetadata{Format: b.Format, DType: b.DType, Shape: b.Shape, Order: b.Order}
	_, err := metadata.normalized()
	return err
}

func (w *PythonWorker) removeBlobFile(blob PythonBlob) {
	if w == nil {
		return
	}
	w.dataMu.Lock()
	defer w.dataMu.Unlock()
	if err := blob.validate(w.config.MaxBlobBytes); err != nil {
		return
	}
	_ = os.Remove(filepath.Join(w.dataDir, blob.Name))
	if size, ok := w.blobs[blob.Name]; ok {
		delete(w.blobs, blob.Name)
		w.dataBytes -= size
		if w.dataBytes < 0 {
			w.dataBytes = 0
		}
	}
}

// Call invokes an operation in the worker. A deadline or cancellation kills
// the current child, because a non-cooperative Python extension cannot be
// safely interrupted from Go. The next call transparently starts a fresh
// worker.
func (w *PythonWorker) Call(ctx context.Context, operation string, args []Value) Result {
	return w.CallSession(ctx, "default", operation, args)
}

// CallSession is Call with an explicit logical session label. The bundled
// worker isolates object handles and mappings by session. A restart discards
// all session objects; stale handles fail rather than aliasing new objects.
func (w *PythonWorker) CallSession(ctx context.Context, session, operation string, args []Value) Result {
	if w == nil {
		return Failed(errors.New("nil python worker"))
	}
	if w.queue == nil || w.callSlot == nil {
		return Failed(errors.New("python worker is not initialized"))
	}
	if err := checkContext(ctx); err != nil {
		return Failed(err)
	}
	if operation == "" {
		return Failed(errors.New("python operation is empty"))
	}
	select {
	case w.queue <- struct{}{}:
		defer func() { <-w.queue }()
	case <-ctx.Done():
		return Failed(ctx.Err())
	}

	select {
	case w.callSlot <- struct{}{}:
		defer func() { <-w.callSlot }()
	case <-ctx.Done():
		return Failed(ctx.Err())
	}

	proc, err := w.ensureProcess(ctx)
	if err != nil {
		return Failed(err)
	}
	requestID := strconv.FormatUint(w.nextID.Add(1), 10)
	if args == nil {
		args = []Value{}
	}
	request := pythonRequest{Type: "request", ID: requestID, Op: operation, Args: args, Session: session}
	if deadline, ok := ctx.Deadline(); ok {
		request.Deadline = deadline.UTC().Format(time.RFC3339Nano)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Failed(fmt.Errorf("encode python request %s (%s): %w", requestID, operation, err))
	}
	if len(encoded) > w.config.MaxMessageBytes {
		return Failed(fmt.Errorf("python request %s (%s) exceeds %d bytes", requestID, operation, w.config.MaxMessageBytes))
	}
	encoded = append(encoded, '\n')
	if _, err := proc.stdin.Write(encoded); err != nil {
		w.replaceProcess(proc)
		return Failed(fmt.Errorf("write python request %s (%s): %w", requestID, operation, err))
	}

	responseCh := make(chan struct {
		response pythonResponse
		err      error
	}, 1)
	go func() {
		for {
			line, readErr := readLine(proc.stdout, w.config.MaxMessageBytes)
			if readErr != nil {
				responseCh <- struct {
					response pythonResponse
					err      error
				}{err: readErr}
				return
			}
			var response pythonResponse
			if err := json.Unmarshal(line, &response); err != nil {
				responseCh <- struct {
					response pythonResponse
					err      error
				}{err: fmt.Errorf("decode python response %s (%s): %w", requestID, operation, err)}
				return
			}
			if response.Type == "progress" {
				continue
			}
			responseCh <- struct {
				response pythonResponse
				err      error
			}{response: response}
			return
		}
	}()

	select {
	case result := <-responseCh:
		if err := ctx.Err(); err != nil {
			w.replaceProcess(proc)
			return Failed(err)
		}
		if result.err != nil {
			w.replaceProcess(proc)
			return Failed(fmt.Errorf("read python response %s (%s): %w", requestID, operation, result.err))
		}
		if result.response.ID != requestID {
			w.replaceProcess(proc)
			return Failed(fmt.Errorf("python response ID mismatch for %s (%s): got %q", requestID, operation, result.response.ID))
		}
		if !result.response.OK {
			if result.response.Error == nil {
				return Failed(fmt.Errorf("python operation %q failed without an error", operation))
			}
			return Failed(&PythonError{Operation: operation, RequestID: requestID, Type: result.response.Error.Type, Message: result.response.Error.Message})
		}
		var value Value
		if len(result.response.Value) > 0 && string(result.response.Value) != "null" {
			if err := json.Unmarshal(result.response.Value, &value); err != nil {
				return Failed(fmt.Errorf("decode python value %s (%s): %w", requestID, operation, err))
			}
		}
		return Ready(value)
	case <-ctx.Done():
		// Give a cooperative worker one cancellation frame, then close and kill
		// it so an uncooperative extension cannot contaminate the next request.
		cancelFrame, _ := json.Marshal(map[string]string{"type": "cancel", "id": requestID})
		_, _ = proc.stdin.Write(append(cancelFrame, '\n'))
		w.replaceProcess(proc)
		return Failed(ctx.Err())
	}
}

func (w *PythonWorker) ensureProcess(ctx context.Context) (*pythonProcess, error) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.closed {
		return nil, errors.New("python worker is closed")
	}
	if w.proc != nil {
		return w.proc, nil
	}
	proc, err := w.start(ctx)
	if err != nil {
		return nil, err
	}
	w.proc = proc
	w.pythonVersion = proc.python
	w.capabilities = append([]string(nil), proc.capabilities...)
	return proc, nil
}

func (w *PythonWorker) replaceProcess(proc *pythonProcess) {
	if proc == nil {
		return
	}
	w.stateMu.Lock()
	if w.proc == proc {
		w.proc = nil
	}
	w.stateMu.Unlock()
	killPythonProcess(proc)
}

// Close stops the worker and releases its child process. It is safe to call
// more than once and should normally be used with defer.
func (w *PythonWorker) Close() error {
	if w == nil {
		return nil
	}
	if w.callSlot == nil {
		return nil
	}
	w.callSlot <- struct{}{}
	defer func() { <-w.callSlot }()
	w.stateMu.Lock()
	if w.closed {
		w.stateMu.Unlock()
		return nil
	}
	w.closed = true
	proc := w.proc
	w.proc = nil
	w.stateMu.Unlock()
	if proc != nil {
		killPythonProcess(proc)
	}
	w.dataMu.Lock()
	if w.dataDirOwned && w.dataDir != "" {
		_ = os.RemoveAll(w.dataDir)
	}
	w.blobs = make(map[string]int64)
	w.dataBytes = 0
	w.dataMu.Unlock()
	return nil
}

func killPythonProcess(proc *pythonProcess) {
	if proc == nil || proc.cmd == nil {
		return
	}
	_ = proc.stdin.Close()
	if proc.cmd.Process != nil {
		_ = proc.cmd.Process.Kill()
	}
	_ = proc.cmd.Wait()
}

func readLine(r *bufio.Reader, max int) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if len(line) > max {
		return nil, fmt.Errorf("protocol line exceeds %d bytes", max)
	}
	if err != nil {
		return nil, err
	}
	return bytes.TrimSpace(line), nil
}

func readLineContext(ctx context.Context, r *bufio.Reader, timeout time.Duration, max int) ([]byte, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	result := make(chan struct {
		line []byte
		err  error
	}, 1)
	go func() {
		line, err := readLine(r, max)
		result <- struct {
			line []byte
			err  error
		}{line: line, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case out := <-result:
		return out.line, out.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("timeout after %s", timeout)
	}
}

// RegisterPython registers a worker operation on an existing Host. The host
// owns the operation name used by LIP; operation is the name understood by
// Python. This keeps Python imports out of the LIP language and makes the
// adapter replaceable.
func (h Host) RegisterPython(name string, worker *PythonWorker, operation string, effect Effect) {
	if worker == nil {
		panic("nil python worker")
	}
	if h.ops == nil {
		panic("runtime.Host is not initialized")
	}
	if effect == EffectUnknown {
		effect = EffectExternalWrite
	}
	h.ops[name] = func(ctx context.Context, args []Value) Result {
		return worker.Call(ctx, operation, args)
	}
	h.effects[name] = effect
}

func (h Host) RegisterPythonPure(name string, worker *PythonWorker, operation string) {
	h.RegisterPython(name, worker, operation, EffectPure)
}

func (h Host) RegisterPythonReadOnly(name string, worker *PythonWorker, operation string) {
	h.RegisterPython(name, worker, operation, EffectReadOnly)
}

// RegisterPythonSession binds a fixed session label to a Host operation. It
// is useful when a Flow should explicitly select a warmed model or dataset.
func (h Host) RegisterPythonSession(name string, worker *PythonWorker, session, operation string, effect Effect) {
	if worker == nil {
		panic("nil python worker")
	}
	if h.ops == nil {
		panic("runtime.Host is not initialized")
	}
	if effect == EffectUnknown {
		effect = EffectExternalWrite
	}
	h.ops[name] = func(ctx context.Context, args []Value) Result {
		return worker.CallSession(ctx, session, operation, args)
	}
	h.effects[name] = effect
}

// PythonCapabilities returns the capabilities reported by a worker during
// startup. It is useful for diagnostics and optional dependency checks.
func (w *PythonWorker) PythonCapabilities(ctx context.Context) ([]string, error) {
	if w == nil {
		return nil, errors.New("nil python worker")
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.proc == nil {
		return nil, errors.New("python worker is not running")
	}
	return append([]string(nil), w.capabilities...), nil
}

// PythonVersion reports the child interpreter version from the startup
// handshake.
func (w *PythonWorker) PythonVersion() string {
	if w == nil {
		return ""
	}
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	return w.pythonVersion
}

//go:embed python_worker.py
var bundledPythonWorker string
