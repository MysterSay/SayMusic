# Say Music local changes

Based on github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808. Upstream license retained.

Added SetTransparentBackground(view WebView) using Controller2 default background alpha=0, plus Controller2.Release() to balance QueryInterface. This permits Windows DWM materials behind the island window. No other upstream behavior changed.

1.5.2: WM_SHOWWINDOW synchronizes controller visibility; RefreshSurface restores bounds/visibility after native resizing/show; navigation completion status callback added.

1.5.17: added ReplaceSessionCookies using ICoreWebView2CookieManager via CoreWebView2_3. Secure and HttpOnly flags are retained; native cookies are kept out of page scripts. The host validates original expiry before restoring session cookies. WindowOptions.Hidden skips ShowWindow/SetFocus for a service helper window; existing windows retain their behavior.

1.5.17: Dispatch thread messages now carry the target HWND; a shared UI message loop drains the matching WebView's queue. This is required for Bind promise replies from a secondary helper WebView. Zero-target messages retain the original main-view behavior.
