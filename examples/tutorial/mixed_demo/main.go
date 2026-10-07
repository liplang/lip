package main

import (
	"context"
	"fmt"
	"os"

	"lipalpha/examples/tutorial/mixedflow"
	"lipalpha/runtime"
)

func loadText(ctx context.Context, args []runtime.Value) runtime.Result {
	if err := ctx.Err(); err != nil {
		return runtime.Failed(err)
	}
	if len(args) != 1 {
		return runtime.Failed(fmt.Errorf("load_text expects one path"))
	}
	if err := runtime.CheckType(args[0], "string"); err != nil {
		return runtime.Failed(err)
	}
	data, err := os.ReadFile(args[0].(string))
	if err != nil {
		return runtime.Failed(err)
	}
	if err := ctx.Err(); err != nil {
		return runtime.Failed(err)
	}
	return runtime.Ready(string(data))
}

func runMain() error {
	ctx := context.Background()
	worker, err := runtime.NewPythonWorker(ctx, runtime.PythonWorkerConfig{})
	if err != nil {
		return err
	}
	defer worker.Close()
	host := runtime.NewPythonHost(worker)
	host.RegisterReadOnly("load_text", loadText)
	file, err := os.CreateTemp("", "lip-roots-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.WriteString("1 4 9\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	value, _, err := mixedflow.Run(ctx, host, map[string]runtime.Value{"path": file.Name()})
	if err != nil {
		return err
	}
	text, err := runtime.FormatValue(value)
	if err != nil {
		return err
	}
	fmt.Println(text)
	return nil
}

func main() {
	if err := runMain(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
