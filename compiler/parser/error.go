package parser

import (
	"errors"
	"fmt"

	"lipalpha/compiler/token"
)

// Error retains whether valid input is unfinished, so interactive callers do
// not have to infer continuation from the spelling of a diagnostic.
type Error struct {
	Pos        token.Pos
	Message    string
	Incomplete bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Column, e.Message)
}

func IsIncomplete(err error) bool {
	var syntax *Error
	return errors.As(err, &syntax) && syntax.Incomplete
}
