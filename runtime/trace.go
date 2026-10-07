package runtime

import (
	"encoding/json"
	"io"
)

// WriteTrace exports only lifecycle metadata, never node values or arguments.
func WriteTrace(writer io.Writer, events []TraceEvent, runError error) error {
	type event struct {
		Tick   uint64 `json:"tick"`
		Node   string `json:"node"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	document := struct {
		Schema string  `json:"schema"`
		Events []event `json:"events"`
		Error  string  `json:"error,omitempty"`
	}{Schema: "lip.trace.v1", Events: make([]event, 0, len(events))}
	for _, item := range events {
		document.Events = append(document.Events, event{item.Tick, item.Node, item.Status.String(), item.Reason})
	}
	if runError != nil {
		document.Error = runError.Error()
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}
