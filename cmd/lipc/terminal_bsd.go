//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "syscall"

func terminalRequests() (uintptr, uintptr) { return syscall.TIOCGETA, syscall.TIOCSETA }

// FIONREAD is shared by the supported BSD terminal ABIs.
func terminalReadRequest() uintptr { return 0x4004667f }
