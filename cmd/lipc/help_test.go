package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandHelpCoversEveryCommand(t *testing.T) {
	for _, command := range []string{"run", "emit-go", "build", "serve", "repl", "learn"} {
		t.Run(command, func(t *testing.T) {
			var help bytes.Buffer
			printCommandHelp(&help, command)
			text := help.String()
			if !strings.Contains(text, "Usage: lipc "+command) {
				t.Fatalf("help for %s has no usage line:\n%s", command, text)
			}
			if !strings.Contains(text, "Examples:") {
				t.Fatalf("help for %s has no example:\n%s", command, text)
			}
		})
	}
}

func TestHelpSpellings(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		if !isHelpArg(arg) {
			t.Errorf("%q was not recognized as help", arg)
		}
	}
	if commandHelpRequested([]string{"-h"}) || commandHelpRequested([]string{"--help"}) {
		t.Fatal("flag help should be handled by the command FlagSet")
	}
	if commandHelpRequested([]string{"help", "extra"}) {
		t.Fatal("help with extra arguments is not a command help request")
	}
}
