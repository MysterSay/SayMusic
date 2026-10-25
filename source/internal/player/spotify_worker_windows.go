//go:build windows

package player

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	webview2 "github.com/jchv/go-webview2"
	"net/url"
	"path/filepath"
	"saymusic/internal/spotifyworker"
	"strings"
	"time"
)

var spotifyCollectorJS = spotifyworker.Collector

type spotifyWorkerMessage struct {
	spotifyReport
	ID      string `json:"id"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

// The worker shares the application's UI message loop but has its own profile.
// It is kept alive between jobs; destroying a window in this WebView wrapper
// posts WM_QUIT, so it is only destroyed after the main loop has ended.
func installSpotifyWorker(owner webview2.WebView, dir string, a *App) func() {
	var worker webview2.WebView
	profiles := map[string]webview2.WebView{}
	type work struct {
		id, url  string
		ctx      context.Context
		messages chan spotifyWorkerMessage
	}
	var active *work
	reportName := "__saySpotifyReport_" + randomID()
	clear := func() {
		for _, view := range profiles {
			view.Navigate("about:blank")
			webview2.ReplaceSessionCookies(view, nil)
		}
		active = nil
	}
	collect := func(ctx context.Context, raw string, cookies []browserCookie, progress func(string)) (spotifyReport, error) {
		messages := make(chan spotifyWorkerMessage, 64)
		id := randomID()
		owner.Dispatch(func() {
			if ctx.Err() != nil {
				return
			}
			// Separate storage as well as cookies when switching accounts.
			// Spotify's local database must never supply another account's list.
			sessionKey := ""
			for _, c := range cookies {
				if c.Name == "sp_dc" {
					sessionKey = c.Value
					break
				}
			}
			digest := sha256.Sum256([]byte(sessionKey))
			profile := fmt.Sprintf("%x", digest[:12])
			selected := profiles[profile]
			if worker != nil && worker != selected {
				worker.Navigate("about:blank")
				webview2.ReplaceSessionCookies(worker, nil)
			}
			worker = selected
			if worker == nil {
				worker = webview2.NewWithOptions(webview2.WebViewOptions{DataPath: filepath.Join(dir, "SpotifyWebView", profile), AutoFocus: false, WindowOptions: webview2.WindowOptions{Hidden: true, Title: "Say Music · Spotify", Width: 1100, Height: 800, IconId: 2}})
				if worker == nil {
					messages <- spotifyWorkerMessage{Type: "error", Error: "Не вдалося створити WebView2 для Spotify"}
					return
				}
				u32.NewProc("ShowWindow").Call(uintptr(worker.Window()), 0)
				webview2.RefreshSurface(worker)
				if e := worker.Bind(reportName, func(raw string) error {
					if len(raw) > 16<<20 || active == nil || active.ctx.Err() != nil {
						return nil
					}
					var msg spotifyWorkerMessage
					if json.Unmarshal([]byte(raw), &msg) != nil || msg.ID != active.id {
						return nil
					}
					if msg.Type == "result" || msg.Type == "error" {
						select {
						case active.messages <- msg:
						default: // Ensure a final result is never dropped behind progress.
							select {
							case <-active.messages:
							default:
							}
							active.messages <- msg
						}
					} else if msg.Type == "progress" {
						select {
						case active.messages <- msg:
						default:
						}
					}
					return nil
				}); e != nil {
					messages <- spotifyWorkerMessage{Type: "error", Error: e.Error()}
					return
				}
				profiles[profile] = worker
				bindingLiteral, _ := json.Marshal(reportName)
				worker.Init(strings.Replace(spotifyCollectorJS, "__SAY_REPORT_BINDING__", string(bindingLiteral), 1))
			}
			// Stop any old page before replacing cookies, to isolate account changes.
			worker.Navigate("about:blank")
			native := make([]webview2.SessionCookie, 0, len(cookies))
			for _, c := range cookies {
				native = append(native, webview2.SessionCookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Secure: c.Secure, HTTPOnly: c.HTTPOnly})
			}
			if e := webview2.ReplaceSessionCookies(worker, native); e != nil {
				messages <- spotifyWorkerMessage{Type: "error", Error: "Не вдалося застосувати сесію Spotify у WebView2"}
				return
			}
			active = &work{id: id, url: raw, ctx: ctx, messages: messages}
			target, _ := url.Parse(raw)
			target.Fragment = "say-music-job=" + id
			worker.Navigate(target.String())
		})
		defer owner.Dispatch(func() {
			if active != nil && active.id == id {
				active = nil
				if worker != nil {
					worker.Navigate("about:blank")
				}
			}
		})
		idle := time.NewTimer(90 * time.Second)
		defer idle.Stop()
		for {
			select {
			case <-idle.C:
				return spotifyReport{}, errors.New("Spotify не завантажив список. Перевір мережу, увійди у браузері та скопіюй сесію повторно")
			case <-ctx.Done():
				return spotifyReport{}, ctx.Err()
			case msg := <-messages:
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(90 * time.Second)
				switch msg.Type {
				case "progress":
					progress(msg.Message)
				case "error":
					return spotifyReport{}, errors.New(msg.Error)
				case "result":
					return msg.spotifyReport, nil
				}
			}
		}
	}
	a.spotifyMu.Lock()
	a.spotifyCollect = collect
	a.spotifyClear = func() { owner.Dispatch(clear) }
	a.spotifyMu.Unlock()
	return func() {
		a.spotifyMu.Lock()
		a.spotifyCollect = nil
		a.spotifyClear = nil
		if a.spotifyCancel != nil {
			a.spotifyCancel()
		}
		a.spotifyMu.Unlock()
		active = nil
		for _, view := range profiles {
			view.Destroy()
		}
	}
}
