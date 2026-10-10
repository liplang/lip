package compiler

import (
	"fmt"
	"strings"
)

func emitForNode(node Node, bodyName string, b *strings.Builder) error {
	fmt.Fprintf(b, "\tg.Add(runtime.NodeSpec{Name: %s, Effect: runtime.EffectExternalWrite, Deps: %s, Gates: %s, Eval: func(ctx context.Context, values map[string]runtime.Value) runtime.Result {\n", quote(node.Name), stringSlice(node.Deps), stringSlice(node.Gates))
	emitter := newValueEmitter(func(name string) string { return "values[" + quote(name) + "]" }, "\t\t")
	if node.Expr != nil {
		source, err := emitter.value(node.Expr)
		if err != nil {
			return fmt.Errorf("node %s: for source: %w", node.Name, err)
		}
		b.WriteString(emitter.code())
		fmt.Fprintf(b, "\n\t\t_, loopErr := runtime.ForEach(ctx, %s, func(ctx context.Context, item runtime.Value) runtime.Result {\n\t\t\tinputs := map[string]runtime.Value{%s: item,", source, quote(node.For.Variable))
	} else {
		b.WriteString("\n")
		b.WriteString("\t\t_, loopErr := runtime.ForEver(ctx, func(ctx context.Context) runtime.Result {\n\t\t\tinputs := map[string]runtime.Value{")
	}
	for _, name := range node.For.Body.Params {
		if name != node.For.Variable {
			fmt.Fprintf(b, "%s: values[%s],", quote(name), quote(node.For.Captures[name]))
		}
	}
	b.WriteString("}\n\t\t\t_, trace, err := ")
	fmt.Fprintf(b, "%s(host).Run(ctx, inputs)\n", bodyName)
	b.WriteString("\t\t\tif err != nil { for _, event := range trace { if event.Status != runtime.Error { continue }; switch event.Node {\n")
	for _, inner := range node.For.Body.Nodes {
		fmt.Fprintf(b, "\t\t\tcase %s: err = runtime.WithSource(err, %d, %d)\n", quote(inner.Name), inner.Pos.Line, inner.Pos.Column)
	}
	b.WriteString("\t\t\t}; break }; return runtime.Failed(err) }; return runtime.Ready(nil)\n\t\t})\n\t\tif loopErr != nil { return runtime.Failed(loopErr) }; return runtime.Ready(nil)\n\t},")
	emitLoopGate(node, b)
	b.WriteString("})\n")
	return nil
}

func emitLoopControl(node Node, b *strings.Builder) {
	control := "BreakLoop"
	if node.LoopControl == "continue" {
		control = "ContinueLoop"
	}
	fmt.Fprintf(b, "\tg.Add(runtime.NodeSpec{Name: %s, Effect: runtime.EffectExternalWrite, Gates: %s, Eval: func(ctx context.Context, values map[string]runtime.Value) runtime.Result { return runtime.%s() },", quote(node.Name), stringSlice(node.Gates), control)
	emitLoopGate(node, b)
	b.WriteString("})\n")
}

func emitLoopGate(node Node, b *strings.Builder) {
	if len(node.Gates) == 0 {
		return
	}
	b.WriteString(" Gate: func(values map[string]runtime.Value) (bool, error) {\n")
	for index, gate := range node.Gates {
		fmt.Fprintf(b, "\t\tok%d, err := runtime.Bool(values[%s]); if err != nil { return false, err }; if !ok%d { return false, nil }\n", index, quote(gate), index)
	}
	b.WriteString("\t\treturn true, nil\n\t},")
}
