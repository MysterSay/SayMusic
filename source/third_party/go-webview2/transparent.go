//go:build windows

package webview2

import (
	"errors"
	"github.com/jchv/go-webview2/pkg/edge"
)

func SetTransparentBackground(view WebView) error {
	w, ok := view.(*webview)
	if !ok {
		return errors.New("unsupported view")
	}
	browser, ok := w.browser.(*edge.Chromium)
	if !ok {
		return errors.New("unsupported browser")
	}
	controller := browser.GetController().GetICoreWebView2Controller2()
	if controller == nil {
		return errors.New("Controller2 unavailable")
	}
	defer controller.Release()
	return controller.PutDefaultBackgroundColor(edge.COREWEBVIEW2_COLOR{})
}

// RefreshSurface must run on the window's UI thread after changing HWND bounds
// or showing a previously hidden controller. It does not navigate or reset JS.
func RefreshSurface(view WebView) error {
	w, ok := view.(*webview)
	if !ok {
		return errors.New("unsupported view")
	}
	b, ok := w.browser.(*edge.Chromium)
	if !ok {
		return errors.New("unsupported browser")
	}
	if err := b.Show(); err != nil {
		return err
	}
	b.Resize()
	return b.NotifyParentWindowPositionChanged()
}

// OnNavigation reports completion on the UI thread, including failed requests.
func OnNavigation(view WebView, callback func(bool, int32)) {
	w, ok := view.(*webview)
	if !ok {
		return
	}
	b, ok := w.browser.(*edge.Chromium)
	if !ok {
		return
	}
	b.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, args *edge.ICoreWebView2NavigationCompletedEventArgs) {
		success, status := args.Result()
		callback(success, status)
	}
}

// SetSurfaceBackground selects opaque compositing for reliable redraw on hosts
// where DWM accent blur and a transparent WebView2 surface leave stale pixels.
func SetSurfaceBackground(view WebView, transparent, light bool) error {
	w, ok := view.(*webview)
	if !ok {
		return errors.New("unsupported view")
	}
	b, ok := w.browser.(*edge.Chromium)
	if !ok {
		return errors.New("unsupported browser")
	}
	c := b.GetController().GetICoreWebView2Controller2()
	if c == nil {
		return errors.New("Controller2 unavailable")
	}
	defer c.Release()
	color := edge.COREWEBVIEW2_COLOR{A: 255, R: 24, G: 25, B: 29}
	if light {
		color = edge.COREWEBVIEW2_COLOR{A: 255, R: 239, G: 240, B: 244}
	}
	if transparent {
		color = edge.COREWEBVIEW2_COLOR{}
	}
	return c.PutDefaultBackgroundColor(color)
}
