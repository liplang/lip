# Python-backed scientific Flow

`flow.lip` stays ordinary LIP. `main.go` starts a resident Python Worker and
enables the generic Python fallback; dotted operations such as `numpy.sum`,
`numpy.mean`, and `pandas.describe` need no per-library registration. The Go
example uses a checksummed local read-only blob for the numeric input, while the
generated Flow receives ordinary JSON-compatible results back from the worker.

Run it from the repository root:

```bash
GOCACHE=/tmp/lip-gocache go run ./examples/python
```

The bundled worker needs Python 3. NumPy and Pandas enable the operations used by
the example; the worker also has dependency-free `sum`, `mean`, `dot`, and
`matrix_multiply` operations for smoke tests. `flow.lip` declares
`require python "numpy"` and `require python "pandas"`; this is metadata and
does not install packages. Standalone generated programs start the default Worker
for a declared Python dependency; the Go example keeps explicit Worker ownership
so it can use the blob data plane.

Dotted operation names are resolved by the generic Python Host. The same mechanism
works for any installed package, for example `math`, `networkx`, `sympy`, `sklearn`,
`torch`, `jax`, `keras`, `cv2`, `transformers`, `statsmodels`, `QuantLib`, or a
package owned by your project. Install those dependencies in the interpreter
selected by `PythonWorkerConfig`; the Go runtime does not maintain a per-library
registry.
