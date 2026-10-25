//go:build windows

package player

import (
	"errors"
	"fmt"
	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var processJob windows.Handle
var instanceMutex windows.Handle

func initProcessGroup() bool {
	name, _ := windows.UTF16PtrFromString("Local\\SayMusic.Desktop.SingleInstance")
	mutex, e := windows.CreateMutex(nil, false, name)
	if e == windows.ERROR_ALREADY_EXISTS {
		if mutex != 0 {
			windows.CloseHandle(mutex)
		}
		title, _ := syscall.UTF16PtrFromString("Say Music")
		user32 := syscall.NewLazyDLL("user32.dll")
		hwnd, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
		if hwnd != 0 {
			user32.NewProc("ShowWindow").Call(hwnd, 9)
			user32.NewProc("SetForegroundWindow").Call(hwnd)
			user32.NewProc("PostMessageW").Call(hwnd, 0x8002, 0, 0)
		}
		return false
	}
	instanceMutex = mutex

	id, _ := syscall.UTF16PtrFromString("SayMusic.Desktop.Player")
	syscall.NewLazyDLL("shell32.dll").NewProc("SetCurrentProcessExplicitAppUserModelID").Call(uintptr(unsafe.Pointer(id)))
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return true
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		windows.CloseHandle(job)
		return true
	}
	if e = windows.AssignProcessToJobObject(job, windows.CurrentProcess()); e != nil {
		windows.CloseHandle(job)
		return true
	}
	processJob = job
	return true
}
func quiet(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func focusMusicWindow() {
	title, _ := windows.UTF16PtrFromString("Say Music")
	user32 := windows.NewLazySystemDLL("user32.dll")
	hwnd, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd != 0 {
		user32.NewProc("ShowWindow").Call(hwnd, 9)
		user32.NewProc("SetForegroundWindow").Call(hwnd)
		user32.NewProc("PostMessageW").Call(hwnd, 0x8002, 0, 0)
	}
}
func openWindow(link, dir string, done <-chan struct{}, a *App) bool {
	disableBackgroundThrottling()
	w := webview2.NewWithOptions(webview2.WebViewOptions{Debug: false, AutoFocus: true, DataPath: filepath.Join(dir, "WebView"), WindowOptions: webview2.WindowOptions{Title: "Say Music", Width: 1360, Height: 900, Center: true, IconId: 2}})
	if w == nil {
		showFatal(a.uiText("Не вдалося створити вікно. Встанови Microsoft Edge WebView2 Runtime через Install-Dependencies.ps1."))
		return true
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	applyMainWindowTheme(hwnd, a.config())
	round := int32(2)
	dwm := syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	dwm.Call(hwnd, 33, uintptr(unsafe.Pointer(&round)), 4)

	w.SetSize(800, 600, webview2.HintMin)
	cleanupEmbedded := installEmbeddedAuth(w, dir, a)
	defer cleanupEmbedded()
	cleanupSpotify := installSpotifyWorker(w, dir, a)
	defer cleanupSpotify()
	cleanupDesktop := installDesktop(w, link, dir, a)
	defer cleanupDesktop()
	w.Navigate(link)
	go func() { <-done; w.Dispatch(func() { w.Terminate() }) }()
	w.Run()
	return true
}
func browserExecutable(name string) (string, error) {
	rel := ""
	exe := ""
	switch name {
	case "chrome":
		rel = filepath.Join("Google", "Chrome", "Application", "chrome.exe")
		exe = "chrome.exe"
	case "edge":
		rel = filepath.Join("Microsoft", "Edge", "Application", "msedge.exe")
		exe = "msedge.exe"
	case "brave":
		rel = filepath.Join("BraveSoftware", "Brave-Browser", "Application", "brave.exe")
		exe = "brave.exe"
	default:
		return "", errors.New("Невідомий браузер")
	}
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
		p := filepath.Join(base, rel)
		if st, e := os.Stat(p); e == nil && !st.IsDir() {
			return p, nil
		}
	}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			key, e := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+exe, registry.QUERY_VALUE|view)
			if e != nil {
				continue
			}
			path, _, e := key.GetStringValue("")
			key.Close()
			if e == nil {
				if st, err := os.Stat(path); err == nil && !st.IsDir() {
					return path, nil
				}
			}
		}
	}
	if p, e := exec.LookPath(exe); e == nil {
		return p, nil
	}
	return "", fmt.Errorf("Браузер %s не знайдено. Встанови його у стандартну папку", name)
}
func replaceFile(src, dst string) error {
	from, e := windows.UTF16PtrFromString(src)
	if e != nil {
		return e
	}
	to, e := windows.UTF16PtrFromString(dst)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func openShared(path string) (*os.File, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(h), path), nil
}
func chooseFolder(language string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ole := syscall.NewLazyDLL("ole32.dll")
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 2)
	if int32(hr) < 0 {
		return "", errors.New("Не вдалося відкрити вибір папки")
	}
	defer ole.NewProc("CoUninitialize").Call()
	title, _ := syscall.UTF16PtrFromString(localized(language, "Папка кешу Say Music"))
	var display [260]uint16
	type browseInfo struct {
		Owner    uintptr
		Root     uintptr
		Display  *uint16
		Title    *uint16
		Flags    uint32
		Callback uintptr
		Param    uintptr
		Image    int32
	}
	bi := browseInfo{Display: &display[0], Title: title, Flags: 0x41}
	shell := syscall.NewLazyDLL("shell32.dll")
	pidl, _, _ := shell.NewProc("SHBrowseForFolderW").Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", nil
	}
	defer ole.NewProc("CoTaskMemFree").Call(pidl)
	var path [32768]uint16
	r, _, _ := shell.NewProc("SHGetPathFromIDListW").Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if r == 0 {
		return "", errors.New("Обери локальну папку")
	}
	return syscall.UTF16ToString(path[:]), nil
}
func showFatal(message string) {
	log.Print(message)
	text, _ := syscall.UTF16PtrFromString("Say Music: " + message)
	title, _ := syscall.UTF16PtrFromString("Say Music — помилка")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

// Normal user browsing must not be terminated with the player's helper job.
func launchBrowser(path, link string) error {
	c := exec.Command(path, link)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB | windows.CREATE_NEW_PROCESS_GROUP}
	if e := c.Start(); e != nil {
		return e
	}
	go c.Wait()
	return nil
}

// The main title bar follows the app theme; the island remains independent.
func applyMainWindowTheme(hwnd uintptr, c Config) {
	dark := int32(1)
	if c.InterfaceTheme == "light" {
		dark = 0
	}
	if c.InterfaceTheme == "system" {
		key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
		if err == nil {
			value, _, err := key.GetIntegerValue("AppsUseLightTheme")
			key.Close()
			if err == nil && value != 0 {
				dark = 0
			}
		}
	}
	syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute").Call(hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4)
}

func windowsAppsUseDarkTheme() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return err != nil || value == 0
}
