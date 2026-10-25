package player

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
)

type EmbeddedAuthStatus struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (a *App) embeddedStatus(provider string, s EmbeddedAuthStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.embeddedStatuses == nil {
		a.embeddedStatuses = map[string]EmbeddedAuthStatus{}
	}
	a.embeddedStatuses[provider] = s
}
func (a *App) storeEmbeddedCookies(provider string, cookies []browserCookie) error {
	if a.config().OfflineOnly {
		return errors.New("Вимкни офлайн-режим")
	}
	if provider == "spotify" {
		return a.storeSpotifySession(cookies)
	}
	if provider != "youtube" {
		return errors.New("Невідомий сервіс")
	}
	jar, ok := cookieJar(cookies)
	if !ok {
		return errors.New("Очікування входу в YouTube Music")
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if err := atomicWrite(filepath.Join(a.dir, "cookies.txt"), jar); err != nil {
		return err
	}
	return a.mutateConfig(func(c *Config) { c.Cookies = true })
}
func (a *App) embeddedAuthAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, "embedded-auth/") {
		return false
	}
	provider := r.URL.Query().Get("provider")
	if provider != "youtube" && provider != "spotify" {
		http.Error(w, "Невідомий сервіс", 400)
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	switch path {
	case "embedded-auth/start":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return true
		}
		a.mu.Lock()
		open := a.embeddedOpen
		a.mu.Unlock()
		if open == nil {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Вікно WebView2 недоступне"})
			return true
		}
		if err := open(provider); err != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return true
		}
		_ = json.NewEncoder(w).Encode(true)
	case "embedded-auth/status":
		a.mu.Lock()
		s := a.embeddedStatuses[provider]
		a.mu.Unlock()
		if s.Status == "" {
			s = EmbeddedAuthStatus{Status: "idle", Message: "Вхід ще не розпочато"}
		}
		_ = json.NewEncoder(w).Encode(s)
	default:
		w.WriteHeader(404)
	}
	return true
}
