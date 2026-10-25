//go:build windows

package player

import (
	"context"
	"fmt"
	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"path/filepath"
	"time"
)

type embeddedLogin struct {
	view         webview2.WebView
	generation   uint64
	active, busy bool
	cancel       context.CancelFunc
}

func installEmbeddedAuth(owner webview2.WebView, dir string, a *App) func() {
	views := map[string]*embeddedLogin{}
	user := windows.NewLazySystemDLL("user32.dll")
	show := user.NewProc("ShowWindow")
	focus := user.NewProc("SetForegroundWindow")
	a.mu.Lock()
	a.embeddedOpen = func(provider string) error {
		if a.config().OfflineOnly {
			return fmt.Errorf("Вимкни офлайн-режим перед входом")
		}
		a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "waiting", Message: "Відкриваю вікно входу…"})
		owner.Dispatch(func() {
			x := views[provider]
			if x == nil {
				x = &embeddedLogin{}
				views[provider] = x
			}
			if x.cancel != nil {
				x.cancel()
			}
			x.generation++
			generation := x.generation
			x.active = true
			title, target := "YouTube Music", "https://music.youtube.com/"
			if provider == "spotify" {
				title, target = "Spotify", "https://accounts.spotify.com/login?continue=https%3A%2F%2Fopen.spotify.com%2F"
			}
			if x.view == nil {
				x.view = webview2.NewWithOptions(webview2.WebViewOptions{AutoFocus: true, DataPath: filepath.Join(dir, "AccountWebView", provider), WindowOptions: webview2.WindowOptions{Title: "Say Music · Вхід " + title, Width: 1060, Height: 780, Center: true, IconId: 2, HideOnClose: true, OnClose: func() {
					x.active = false
					if x.cancel != nil {
						x.cancel()
					}
					a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "cancelled", Message: "Вікно входу закрито"})
				}}})
				if x.view == nil {
					x.active = false
					a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "error", Message: "Не вдалося створити WebView2"})
					return
				}
				applyMainWindowTheme(uintptr(x.view.Window()), a.config())
			}
			a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "waiting", Message: "Увійди в " + title})
			x.view.Navigate(target)
			show.Call(uintptr(x.view.Window()), 5)
			focus.Call(uintptr(x.view.Window()))
			ctx, cancel := context.WithTimeout(a.life, 15*time.Minute)
			x.cancel = cancel
			go func() {
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				defer cancel()
				for {
					select {
					case <-ctx.Done():
						owner.Dispatch(func() {
							if x.active && x.generation == generation {
								x.active = false
								a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "cancelled", Message: "Час очікування входу завершився"})
							}
						})
						return
					case <-ticker.C:
					}
					owner.Dispatch(func() {
						if !x.active || x.generation != generation || x.busy {
							return
						}
						x.busy = true
						uri := "https://music.youtube.com/"
						if provider == "spotify" {
							uri = "https://open.spotify.com/"
						}
						err := webview2.ReadCookies(x.view, uri, func(raw []webview2.CookieSnapshot, err error) {
							x.busy = false
							if !x.active || x.generation != generation {
								return
							}
							if err != nil {
								a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "error", Message: "Не вдалося прочитати сесію WebView2"})
								x.active = false
								cancel()
								return
							}
							cookies := make([]browserCookie, 0, len(raw))
							for _, c := range raw {
								cookies = append(cookies, browserCookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Secure: c.Secure, HTTPOnly: c.HTTPOnly, Expires: c.Expires})
							}
							if provider == "spotify" {
								if _, e := validateSpotifyCookies(cookies); e != nil {
									return
								}
							} else {
								if _, ok := cookieJar(cookies); !ok {
									return
								}
							}
							if err = a.storeEmbeddedCookies(provider, cookies); err != nil {
								x.active = false
								cancel()
								a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "error", Message: err.Error()})
								return
							}
							x.active = false
							cancel()
							show.Call(uintptr(x.view.Window()), 0)
							a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "connected", Message: title + " підключено"})
						})
						if err != nil {
							x.busy = false
							x.active = false
							cancel()
							a.embeddedStatus(provider, EmbeddedAuthStatus{Status: "error", Message: fmt.Sprint(err)})
						}
					})
				}
			}()
		})
		return nil
	}
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		a.embeddedOpen = nil
		a.mu.Unlock()
		for _, x := range views {
			if x.cancel != nil {
				x.cancel()
			}
			if x.view != nil {
				x.view.Destroy()
			}
		}
	}
}
