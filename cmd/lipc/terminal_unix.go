//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func terminalIOCTL(file *os.File, request uintptr, value unsafe.Pointer) error {
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), request, uintptr(value))
	if err != 0 {
		return err
	}
	return nil
}

func terminalInput(file *os.File) bool {
	var state syscall.Termios
	get, _ := terminalRequests()
	return terminalIOCTL(file, get, unsafe.Pointer(&state)) == nil
}

func terminalAvailable(file *os.File) bool {
	var count int32
	return terminalIOCTL(file, terminalReadRequest(), unsafe.Pointer(&count)) == nil && count > 0
}

func rawTerminal(file *os.File) (func(), error) {
	var original syscall.Termios
	get, set := terminalRequests()
	if err := terminalIOCTL(file, get, unsafe.Pointer(&original)); err != nil {
		return nil, err
	}
	state := original
	state.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	state.Cflag |= syscall.CS8
	state.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	state.Cc[syscall.VMIN], state.Cc[syscall.VTIME] = 1, 0
	if err := terminalIOCTL(file, set, unsafe.Pointer(&state)); err != nil {
		return nil, err
	}
	return func() { _ = terminalIOCTL(file, set, unsafe.Pointer(&original)) }, nil
}

func terminalColumns(file *os.File) int {
	var size struct{ Rows, Columns, X, Y uint16 }
	if terminalIOCTL(file, syscall.TIOCGWINSZ, unsafe.Pointer(&size)) == nil && size.Columns > 0 {
		return int(size.Columns)
	}
	return 80
}
