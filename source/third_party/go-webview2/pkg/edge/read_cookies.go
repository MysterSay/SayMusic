//go:build windows

package edge

import (
	"fmt"
	"golang.org/x/sys/windows"
	"sync"
	"sync/atomic"
	"unsafe"
)

type CookieSnapshot struct {
	Name, Value, Domain, Path string
	Secure, HTTPOnly          bool
	Expires                   float64
}
type cookieListVtbl struct {
	_IUnknownVtbl
	Count, At ComProc
}
type cookieList struct{ vtbl *cookieListVtbl }
type cookiesHandlerVtbl struct {
	_IUnknownVtbl
	Invoke ComProc
}
type cookiesHandler struct {
	vtbl *cookiesHandlerVtbl
	refs int32
	done func([]CookieSnapshot, error)
}

var pendingCookies sync.Map
var cookiesVtable = cookiesHandlerVtbl{_IUnknownVtbl{NewComProc(cookieQI), NewComProc(cookieAdd), NewComProc(cookieRelease)}, NewComProc(cookieInvoke)}

func cookieQI(h *cookiesHandler, iid *windows.GUID, out *uintptr) uintptr {
	if out == nil {
		return 0x80004003
	}
	*out = 0
	if iid == nil {
		return 0x80004002
	}
	// IUnknown and ICoreWebView2GetCookiesCompletedHandler.
	if *iid != (windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}) && *iid != (windows.GUID{Data1: 0x5a4f5069, Data2: 0x5c15, Data3: 0x47c3, Data4: [8]byte{0x86, 0x46, 0xf4, 0xde, 0x1c, 0x11, 0x66, 0x70}}) {
		return 0x80004002
	}
	*out = uintptr(unsafe.Pointer(h))
	cookieAdd(h)
	return 0
}
func cookieAdd(h *cookiesHandler) uintptr { return uintptr(atomic.AddInt32(&h.refs, 1)) }
func cookieRelease(h *cookiesHandler) uintptr {
	n := atomic.AddInt32(&h.refs, -1)
	if n == 0 {
		pendingCookies.Delete(uintptr(unsafe.Pointer(h)))
	}
	return uintptr(n)
}
func cookieInvoke(h *cookiesHandler, hr uintptr, list *cookieList) uintptr {
	done := h.done
	h.done = nil
	if done == nil {
		return 0
	}
	if err := cookieResult(hr); err != nil {
		done(nil, err)
		return 0
	}
	if list == nil {
		done(nil, fmt.Errorf("WebView2 returned no cookies"))
		return 0
	}
	var count uint32
	hr, _, _ = list.vtbl.Count.Call(uintptr(unsafe.Pointer(list)), uintptr(unsafe.Pointer(&count)))
	if err := cookieResult(hr); err != nil {
		done(nil, err)
		return 0
	}
	if count > 1000 {
		done(nil, fmt.Errorf("Too many cookies"))
		return 0
	}
	result := make([]CookieSnapshot, 0, count)
	for i := uint32(0); i < count; i++ {
		var c *sessionCookie
		hr, _, _ = list.vtbl.At.Call(uintptr(unsafe.Pointer(list)), uintptr(i), uintptr(unsafe.Pointer(&c)))
		if int32(hr) < 0 || c == nil {
			continue
		}
		var x CookieSnapshot
		valid := true
		for _, field := range []struct {
			proc ComProc
			dst  *string
		}{{c.vtbl.GetName, &x.Name}, {c.vtbl.GetValue, &x.Value}, {c.vtbl.GetDomain, &x.Domain}, {c.vtbl.GetPath, &x.Path}} {
			var raw *uint16
			hr, _, _ = field.proc.Call(uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&raw)))
			if int32(hr) < 0 {
				valid = false
			}
			if raw != nil {
				*field.dst = windows.UTF16PtrToString(raw)
				windows.CoTaskMemFree(unsafe.Pointer(raw))
			}
		}
		var secure, httpOnly, session int32
		c.vtbl.GetIsSecure.Call(uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&secure)))
		c.vtbl.GetIsHTTPOnly.Call(uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&httpOnly)))
		c.vtbl.GetIsSession.Call(uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&session)))
		if session == 0 {
			c.vtbl.GetExpires.Call(uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&x.Expires)))
		} else {
			x.Expires = -1
		}
		x.Secure = secure != 0
		x.HTTPOnly = httpOnly != 0
		c.vtbl.Release.Call(uintptr(unsafe.Pointer(c)))
		if valid {
			result = append(result, x)
		}
	}
	done(result, nil)
	return 0
}

// ReadCookies is UI-thread only, profile-scoped, and returns values only to the host.
func (e *Chromium) ReadCookies(uri string, done func([]CookieSnapshot, error)) error {
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
	u, err := windows.UTF16PtrFromString(uri)
	if err != nil {
		return err
	}
	h := &cookiesHandler{vtbl: &cookiesVtable, refs: 1, done: done}
	pendingCookies.Store(uintptr(unsafe.Pointer(h)), h)
	hr, _, _ = manager.vtbl.GetCookies.Call(uintptr(unsafe.Pointer(manager)), uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(h)))
	cookieRelease(h)
	return cookieResult(hr)
}
