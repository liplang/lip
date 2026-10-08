//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var consoleDLL = syscall.NewLazyDLL("kernel32.dll")
var getConsoleMode = consoleDLL.NewProc("GetConsoleMode")
var setConsoleMode = consoleDLL.NewProc("SetConsoleMode")
var getConsoleInfo = consoleDLL.NewProc("GetConsoleScreenBufferInfo")
var waitConsoleInput = consoleDLL.NewProc("WaitForSingleObject")

func consoleMode(file *os.File) (uint32, error) {
	var mode uint32
	ok, _, err := getConsoleMode.Call(file.Fd(), uintptr(unsafe.Pointer(&mode)))
	if ok == 0 {
		return 0, err
	}
	return mode, nil
}

func terminalInput(file *os.File) bool { _, err := consoleMode(file); return err == nil }
func terminalAvailable(file *os.File) bool {
	result, _, _ := waitConsoleInput.Call(file.Fd(), 0)
	return result == 0
}

func rawTerminal(file *os.File) (func(), error) {
	mode, err := consoleMode(file)
	if err != nil {
		return nil, err
	}
	outputMode, outputErr := consoleMode(os.Stdout)
	if outputErr == nil {
		if ok, _, err := setConsoleMode.Call(os.Stdout.Fd(), uintptr(outputMode|0x0004)); ok == 0 {
			return nil, err
		}
	}
	if ok, _, err := setConsoleMode.Call(file.Fd(), uintptr(mode&^(0x0007|0x0040)|0x0080|0x0200)); ok == 0 {
		if outputErr == nil {
			setConsoleMode.Call(os.Stdout.Fd(), uintptr(outputMode))
		}
		return nil, err
	}
	return func() {
		setConsoleMode.Call(file.Fd(), uintptr(mode))
		if outputErr == nil {
			setConsoleMode.Call(os.Stdout.Fd(), uintptr(outputMode))
		}
	}, nil
}

func terminalColumns(file *os.File) int {
	var info struct {
		SizeX, SizeY, CursorX, CursorY       int16
		Attributes                           uint16
		Left, Top, Right, Bottom, MaxX, MaxY int16
	}
	if ok, _, _ := getConsoleInfo.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&info))); ok != 0 && info.Right >= info.Left {
		return int(info.Right-info.Left) + 1
	}
	return 80
}
