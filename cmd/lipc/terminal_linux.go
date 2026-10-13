//go:build linux

package main

import "syscall"

func terminalRequests() (uintptr, uintptr) { return syscall.TCGETS, syscall.TCSETS }
func terminalReadRequest() uintptr         { return syscall.TIOCINQ }
