//go:build windows

package edge

import (
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

type SessionCookie struct {
	Name, Value, Domain, Path string
	Secure, HTTPOnly          bool
}
type cookieManagerVtbl struct {
	_IUnknownVtbl
	CreateCookie, CopyCookie, GetCookies, AddOrUpdateCookie, DeleteCookie, DeleteCookies, DeleteCookiesWithDomainAndPath, DeleteAllCookies ComProc
}
type sessionCookieManager struct{ vtbl *cookieManagerVtbl }
type sessionCookieVtbl struct {
	_IUnknownVtbl
	GetName, GetValue, PutValue, GetDomain, GetPath, GetExpires, PutExpires, GetIsHTTPOnly, PutIsHTTPOnly, GetSameSite, PutSameSite, GetIsSecure, PutIsSecure, GetIsSession ComProc
}
type sessionCookie struct{ vtbl *sessionCookieVtbl }

func cookieResult(hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("WebView2 cookie HRESULT 0x%08x", uint32(hr))
	}
	return nil
}

// ReplaceSessionCookies must run on the WebView's UI thread. This profile must
// be dedicated to the service: deletion intentionally applies to its cookies.
// Restored cookies remain session cookies; their original expiry is validated
// by the host before every import and is never extended here.
func (e *Chromium) ReplaceSessionCookies(cookies []SessionCookie) error {
	core := e.GetICoreWebView2_3()
	if core == nil {
		return fmt.Errorf("WebView2 cookie manager unavailable")
	}
	defer core.vtbl.Release.Call(uintptr(unsafe.Pointer(core)))
	var manager *sessionCookieManager
	hr, _, _ := core.vtbl.GetCookieManager.Call(uintptr(unsafe.Pointer(core)), uintptr(unsafe.Pointer(&manager)))
	if err := cookieResult(hr); err != nil {
		return err
	}
	if manager == nil {
		return fmt.Errorf("WebView2 cookie manager unavailable")
	}
	defer manager.vtbl.Release.Call(uintptr(unsafe.Pointer(manager)))
	hr, _, _ = manager.vtbl.DeleteAllCookies.Call(uintptr(unsafe.Pointer(manager)))
	if err := cookieResult(hr); err != nil {
		return err
	}
	for _, c := range cookies {
		name, err := windows.UTF16PtrFromString(c.Name)
		if err != nil {
			return err
		}
		value, err := windows.UTF16PtrFromString(c.Value)
		if err != nil {
			return err
		}
		domain, err := windows.UTF16PtrFromString(c.Domain)
		if err != nil {
			return err
		}
		path, err := windows.UTF16PtrFromString(c.Path)
		if err != nil {
			return err
		}
		var cookie *sessionCookie
		hr, _, _ = manager.vtbl.CreateCookie.Call(uintptr(unsafe.Pointer(manager)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(value)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&cookie)))
		if err := cookieResult(hr); err != nil {
			return err
		}
		if cookie == nil {
			return fmt.Errorf("WebView2 failed to create cookie")
		}
		hr, _, _ = cookie.vtbl.PutIsSecure.Call(uintptr(unsafe.Pointer(cookie)), uintptr(boolToInt(c.Secure)))
		err = cookieResult(hr)
		if err == nil {
			hr, _, _ = cookie.vtbl.PutIsHTTPOnly.Call(uintptr(unsafe.Pointer(cookie)), uintptr(boolToInt(c.HTTPOnly)))
			err = cookieResult(hr)
		}
		if err == nil {
			hr, _, _ = manager.vtbl.AddOrUpdateCookie.Call(uintptr(unsafe.Pointer(manager)), uintptr(unsafe.Pointer(cookie)))
			err = cookieResult(hr)
		}
		cookie.vtbl.Release.Call(uintptr(unsafe.Pointer(cookie)))
		if err != nil {
			return err
		}
	}
	return nil
}
