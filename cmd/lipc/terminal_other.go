//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package main

import (
	"fmt"
	"os"
)

func terminalInput(file *os.File) bool     { return false }
func terminalAvailable(file *os.File) bool { return false }
func rawTerminal(file *os.File) (func(), error) {
	return nil, fmt.Errorf("interactive line editing is unavailable on this terminal")
}
func terminalColumns(file *os.File) int { return 80 }
