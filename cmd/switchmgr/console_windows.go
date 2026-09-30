//go:build windows

package main

import (
	"os"
	"syscall"
)

// Windows 版以 GUI 子系统编译（双击不弹黑框）。从命令行运行子命令时，
// 挂到父进程的控制台上，否则 fmt 输出无处可去。
func attachConsole() {
	if len(os.Args) < 2 {
		return
	}
	k32 := syscall.NewLazyDLL("kernel32.dll")
	const attachParent = ^uintptr(0) // ATTACH_PARENT_PROCESS = (DWORD)-1
	if r, _, _ := k32.NewProc("AttachConsole").Call(attachParent); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = f
	}
}
