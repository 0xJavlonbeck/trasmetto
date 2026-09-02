//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func init() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")

	const cpUTF8 = 65001
	_, _, _ = kernel32.NewProc("SetConsoleOutputCP").Call(cpUTF8)

	const enableVirtualTerminalProcessing = 0x0004
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")
	handle := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	if ret, _, _ := getConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode))); ret != 0 {
		_, _, _ = setConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing))
	}
}
