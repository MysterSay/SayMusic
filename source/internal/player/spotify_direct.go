package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type spotifyReport struct {
	Title    string         `json:"title"`
	Tracks   []SpotifyTrack `json:"tracks"`
	Gaps     []ImportGap    `json:"gaps"`
	Complete bool           `json:"complete"`
}
type spotifyCredentials struct {
	Cookies []browserCookie `json:"cookies"`
}

func validateSpotifyCookies(cookies []browserCookie) ([]browserCookie, error) {
	if len(cookies) > 200 {
		return nil, errors.New("Завеликий пакет сесії Spotify")
	}
	valid := []browserCookie{}
	authenticated := false
	for _, c := range cookies {
		domain := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if domain != "spotify.com" && !strings.HasSuffix(domain, ".spotify.com") {
			continue
		}
		if c.Name == "" || c.Value == "" || len(c.Name) > 256 || len(c.Value) > 16384 || strings.ContainsAny(c.Name+c.Value+c.Path, "\r\n\x00\t") || strings.ContainsAny(c.Name, ";= ") || !strings.HasPrefix(c.Path, "/") {
			return nil, errors.New("Некоректна сесія Spotify")
		}
		if c.Expires > 0 && c.Expires <= float64(time.Now().Unix()) {
			continue
		}
		if c.Name == "sp_dc" && (domain == "spotify.com" || domain == "open.spotify.com") {
			authenticated = true
		}
		c.Domain = strings.ToLower(c.Domain)
		valid = append(valid, c)
	}
	if !authenticated {
		return nil, errors.New("У пакеті немає активної сесії Spotify. Онови розширення, увійди на open.spotify.com та скопіюй сесію Spotify повторно")
	}
	return valid, nil
}
func (a *App) readSpotifyCredentials() (c spotifyCredentials, err error) {
	b, err := os.ReadFile(filepath.Join(a.dir, "spotify.dat"))
	if err != nil {
		return c, err
	}
	open := a.openPrivate
	if open == nil {
		open = unprotectSecret
	}
	b, err = open(b)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return
}
func (a *App) writeSpotifyCredentials(c spotifyCredentials) error {
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	seal := a.sealPrivate
	if seal == nil {
		seal = protectSecret
	}
	b, e = seal(b)
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(a.dir, "spotify.dat"), b)
}
func (a *App) spotifyDirect() bool {
	a.spotifyMu.Lock()
	defer a.spotifyMu.Unlock()
	c, e := a.readSpotifyCredentials()
	if e != nil {
		return false
	}
	_, e = validateSpotifyCookies(c.Cookies)
	return e == nil
}
func (a *App) storeSpotifySession(cookies []browserCookie) error {
	if a.config().OfflineOnly {
		return errors.New("Вимкни офлайн-режим")
	}
	clean, e := validateSpotifyCookies(cookies)
	if e != nil {
		return e
	}
	a.spotifyMu.Lock()
	defer a.spotifyMu.Unlock()
	if e = a.writeSpotifyCredentials(spotifyCredentials{Cookies: clean}); e != nil {
		return e
	}
	a.spotifyEpoch++
	if a.spotifyCancel != nil {
		a.spotifyCancel()
	}
	if e = a.mutateConfig(func(c *Config) { c.SpotifyConnected = true }); e != nil {
		return e
	}
	return a.spotifySetState("", "connected", "Сесію Spotify підключено. Списки отримує сам плеєр; браузер можна закрити")
}
func (a *App) disconnectSpotify() {
	a.spotifyMu.Lock()
	defer a.spotifyMu.Unlock()
	a.spotifyEpoch++
	if a.spotifyCancel != nil {
		a.spotifyCancel()
		a.spotifyCancel = nil
	}
	os.Remove(filepath.Join(a.dir, "spotify.dat"))
	if a.spotifyClear != nil {
		a.spotifyClear()
	}
}
func (a *App) startSpotifySync(raw string) (Job, error) {
	if a.config().OfflineOnly {
		return Job{}, errors.New("Вимкни офлайн-режим")
	}
	if _, _, e := spotifyLink(raw); e != nil {
		return Job{}, e
	}
	a.spotifyMu.Lock()
	defer a.spotifyMu.Unlock()
	credentials, e := a.readSpotifyCredentials()
	if e != nil {
		return Job{}, errors.New("Скопіюй сесію Spotify з розширення та встав у Налаштування → Акаунт")
	}
	cookies, e := validateSpotifyCookies(credentials.Cookies)
	if e != nil {
		return Job{}, e
	}
	if a.spotifyCollect == nil {
		return Job{}, errors.New("Отримання Spotify потребує відкритого вікна Say Music з WebView2")
	}
	if !a.spotifyRunMu.TryLock() {
		return Job{}, errors.New("Дочекайся завершення поточного імпорту Spotify")
	}
	ctx, cancel := context.WithTimeout(a.life, 30*time.Minute)
	a.spotifyCancel = cancel
	epoch := a.spotifyEpoch
	collect := a.spotifyCollect
	job := Job{ID: randomID(), Status: "running", Message: "Spotify: отримання списку у плеєрі…"}
	a.mu.Lock()
	copy := job
	a.jobs[job.ID] = &copy
	a.bridgeLastJob = job.ID
	a.mu.Unlock()
	a.spotifySetState(job.ID, "running", job.Message)
	go func() {
		defer a.spotifyRunMu.Unlock()
		defer cancel()
		report, e := collect(ctx, raw, cookies, func(message string) {
			a.spotifyMu.Lock()
			defer a.spotifyMu.Unlock()
			if a.spotifyEpoch != epoch || ctx.Err() != nil {
				return
			}
			a.mu.Lock()
			a.jobs[job.ID].Message = message
			a.mu.Unlock()
			a.spotifySetState(job.ID, "running", message)
		})
		a.spotifyMu.Lock()
		stale := a.spotifyEpoch != epoch || ctx.Err() != nil
		a.spotifyMu.Unlock()
		if stale {
			e = errors.New("Синхронізацію Spotify скасовано або час очікування вичерпано; попередній список збережено")
		}
		if e == nil {
			message := fmt.Sprintf("Spotify: збереження %d пісень у бібліотеку…", len(report.Tracks))
			a.mu.Lock()
			a.jobs[job.ID].Message = message
			a.mu.Unlock()
			a.spotifySetState(job.ID, "running", message)
			_, e = a.importSpotifyReportContext(ctx, raw, report.Title, report.Tracks, report.Complete, report.Gaps)
		}
		a.spotifyMu.Lock()
		defer a.spotifyMu.Unlock()
		if ctx.Err() != nil && e != nil {
			e = errors.New("Синхронізацію Spotify скасовано; попередній список збережено")
		}
		status, message := "done", fmt.Sprintf("Spotify: отримано %d пісень; пропущених позицій — %d", len(report.Tracks), len(report.Gaps))
		if e != nil {
			status = "error"
			message = e.Error()
			if ctx.Err() == nil {
				writeIssue(a.db, TrackIssue{ID: issueKey("spotify:"+raw, 0), Source: "spotify", Title: "Імпорт Spotify", URL: raw, Kind: "import_error", Reason: message})
			}
		}
		a.mu.Lock()
		j := a.jobs[job.ID]
		j.Status = status
		j.Message = message
		j.Count = len(report.Tracks)
		a.bridgeEvent++
		a.mu.Unlock()
		if a.spotifyEpoch == epoch {
			a.spotifySetState(job.ID, status, message)
		}
	}()
	return job, nil
}
