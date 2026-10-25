//go:build windows

package player

import (
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os/exec"
	"runtime"
	"unsafe"
)

// DPAPI uses the current Windows user, not the machine-wide flag.
func protectSecret(b []byte) ([]byte, error)   { return dpapi(b, "CryptProtectData") }
func unprotectSecret(b []byte) ([]byte, error) { return dpapi(b, "CryptUnprotectData") }
func dpapi(b []byte, name string) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("Порожнє захищене сховище")
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	p := windows.NewLazySystemDLL("crypt32.dll").NewProc(name)
	ok, _, e := p.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(b)
	if ok == 0 {
		return nil, e
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
func registerBridge(manifest string) error {
	for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
		for _, browser := range []string{`Google\Chrome`, `Microsoft\Edge`, `BraveSoftware\Brave-Browser`, `Chromium`} {
			k, _, e := registry.CreateKey(registry.CURRENT_USER, `Software\`+browser+`\NativeMessagingHosts\com.saymusic.desktop`, registry.SET_VALUE|view)
			if e != nil {
				return e
			}
			e = k.SetStringValue("", manifest)
			k.Close()
			if e != nil {
				return e
			}
		}
	}
	return nil
}
func unregisterBridge(manifest string) error {
	for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
		root, e := registry.OpenKey(registry.CURRENT_USER, `Software`, registry.ALL_ACCESS|view)
		if e != nil {
			return e
		}
		for _, browser := range []string{`Google\Chrome`, `Microsoft\Edge`, `BraveSoftware\Brave-Browser`, `Chromium`} {
			path := browser + `\NativeMessagingHosts\com.saymusic.desktop`
			k, e := registry.OpenKey(root, path, registry.QUERY_VALUE)
			if e == registry.ErrNotExist {
				continue
			}
			if e != nil {
				root.Close()
				return e
			}
			value, _, e := k.GetStringValue("")
			k.Close()
			if e == nil && value == manifest {
				if e = registry.DeleteKey(root, path); e != nil && e != registry.ErrNotExist {
					root.Close()
					return e
				}
			}
		}
		root.Close()
	}
	return nil
}
func bridgeRegistered(manifest, browser string) bool {
	prefix := map[string]string{"chrome": `Google\Chrome`, "edge": `Microsoft\Edge`, "brave": `BraveSoftware\Brave-Browser`}[browser]
	if prefix == "" {
		return false
	}
	k, e := registry.OpenKey(registry.CURRENT_USER, `Software\`+prefix+`\NativeMessagingHosts\com.saymusic.desktop`, registry.QUERY_VALUE)
	if e != nil {
		return false
	}
	defer k.Close()
	v, _, e := k.GetStringValue("")
	return e == nil && v == manifest
}
func revealFolder(path string) error {
	c := exec.Command("explorer.exe", path)
	quiet(c)
	if e := c.Start(); e != nil {
		return e
	}
	go c.Wait()
	return nil
}
