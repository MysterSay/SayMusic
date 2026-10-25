//go:build windows

package webview2

import (
	"errors"
	"github.com/jchv/go-webview2/pkg/edge"
)

type SessionCookie = edge.SessionCookie

// ReplaceSessionCookies is for dedicated, host-owned service profiles only.
// It must be called on the UI thread; it never exposes cookies to JavaScript.
func ReplaceSessionCookies(view WebView, cookies []SessionCookie) error {
	w, ok := view.(*webview)
	if !ok {
		return errors.New("unsupported view")
	}
	b, ok := w.browser.(*edge.Chromium)
	if !ok {
		return errors.New("unsupported browser")
	}
	return b.ReplaceSessionCookies(cookies)
}

type CookieSnapshot = edge.CookieSnapshot

func ReadCookies(view WebView, uri string, done func([]CookieSnapshot, error)) error {
	w, ok := view.(*webview)
	if !ok {
		return errors.New("unsupported view")
	}
	b, ok := w.browser.(*edge.Chromium)
	if !ok {
		return errors.New("unsupported browser")
	}
	return b.ReadCookies(uri, done)
}
