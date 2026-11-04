//go:build windows

package player

import (
	"golang.org/x/sys/windows"
	"os"
)

func cliConsole() {
	if h, e := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); e == nil && h != 0 && h != windows.InvalidHandle {
		return
	}
	windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole").Call(^uintptr(0))
	if h, e := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); e == nil {
		os.Stdout = os.NewFile(uintptr(h), "stdout")
	}
	if h, e := windows.GetStdHandle(windows.STD_ERROR_HANDLE); e == nil {
		os.Stderr = os.NewFile(uintptr(h), "stderr")
	}
}
