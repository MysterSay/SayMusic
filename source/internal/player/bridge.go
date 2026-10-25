package player

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"saymusic/internal/nativehost"
	"strings"
	"time"
)

type BridgeTask struct{ JobID, URL, Title string }

func (a *App) initBridge(address string) error {
	a.bridgeQueue = make(chan BridgeTask, 100)
	b, _ := json.Marshal(nativehost.Descriptor{URL: address, Token: a.token})
	if e := atomicWrite(filepath.Join(a.dir, "runtime.json"), b); e != nil {
		return e
	}
	go a.bridgeWorker()
	return nil
}
func (a *App) cleanupBridge() {
	p := filepath.Join(a.dir, "runtime.json")
	b, _ := os.ReadFile(p)
	var d nativehost.Descriptor
	if json.Unmarshal(b, &d) == nil && d.Token == a.token {
		_ = os.Remove(p)
	}
}
func (a *App) bridge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var m struct {
		Provider  string          `json:"provider"`
		IDs       []string        `json:"ids"`
		Action    string          `json:"action"`
		Gaps      []ImportGap     `json:"gaps"`
		Tracks    []SpotifyTrack  `json:"tracks"`
		Complete  bool            `json:"complete"`
		Request   string          `json:"request"`
		URL       string          `json:"url"`
		Title     string          `json:"title"`
		Artist    string          `json:"artist"`
		Thumbnail string          `json:"thumbnail"`
		Duration  float64         `json:"duration"`
		Cookies   []browserCookie `json:"cookies"`
	}
	fail := func(e error) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": e.Error()})
	}
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	if e := json.NewDecoder(r.Body).Decode(&m); e != nil {
		fail(e)
		return
	}
	if strings.HasPrefix(m.Action, "spotify/") || m.Action == "status" || m.Action == "open" || m.Action == "auth" || m.Action == "add" {
		a.mu.Lock()
		a.bridgeSeen = time.Now()
		a.mu.Unlock()
	}
	if strings.HasPrefix(m.Action, "spotify/") {
		if m.Action == "spotify/auth" {
			if e := a.storeSpotifySession(m.Cookies); e != nil {
				fail(e)
			} else {
				reply(map[string]any{"ok": true, "message": "Сесію Spotify передано. Списки отримує плеєр"})
			}
			return
		}
		result, e := a.spotifyBridge(m.Action, m.URL, m.Title, m.Request, m.Tracks, m.Complete, m.Gaps)
		if e != nil {
			fail(e)
		} else {
			reply(result)
		}
		return
	}
	switch m.Action {
	case "library/check":
		found, e := a.libraryMembership(m.Provider, m.IDs)
		if e != nil {
			fail(e)
		} else {
			reply(map[string]any{"ok": true, "running": true, "existing": found})
		}
	case "status":
		reply(map[string]any{"ok": true, "running": true, "authenticated": a.config().Cookies, "spotifyConnected": a.config().SpotifyConnected, "version": "1.5.35", "language": a.config().Language})
	case "open":
		focusMusicWindow()
		reply(map[string]any{"ok": true, "message": "Say Music запущено"})
	case "auth":
		if a.config().OfflineOnly {
			fail(errors.New("Вимкни офлайн-режим перед підключенням акаунта"))
			return
		}
		jar, ok := cookieJar(m.Cookies)
		if !ok {
			fail(errors.New("Увійди у YouTube Music у цьому профілі браузера"))
			return
		}
		a.authMu.Lock()
		e := atomicWrite(filepath.Join(a.dir, "cookies.txt"), jar)
		if e == nil {
			e = a.mutateConfig(func(c *Config) { c.Cookies = true })
		}
		a.authMu.Unlock()
		if e != nil {
			fail(e)
			return
		}
		a.mu.Lock()
		a.authStatus = AuthStatus{Status: "done", Message: "Акаунт підключено через розширення"}
		a.bridgeEvent++
		a.mu.Unlock()
		reply(map[string]any{"ok": true, "message": "Акаунт YouTube підключено до Say Music"})
	case "add":
		target, e := youtubeInput(m.URL)
		if e != nil {
			fail(e)
			return
		}
		u, _ := url.Parse(target)
		playlist := u.Query().Get("list")
		id := u.Query().Get("v")
		if u.Hostname() == "youtu.be" {
			id = strings.Trim(u.Path, "/")
		}
		if playlist == "" && validID.MatchString(id) {
			if _, e = a.track(id); e == nil {
				reply(map[string]any{"ok": true, "message": "Трек уже є в бібліотеці Say Music"})
				return
			}
			title := strings.TrimSpace(m.Title)
			if title != "" {
				if len(title) > 1000 {
					title = title[:1000]
				}
				artist := strings.TrimSpace(m.Artist)
				if len(artist) > 600 {
					artist = artist[:600]
				}
				thumbnail := "https://i.ytimg.com/vi/" + id + "/mqdefault.jpg"
				duration := m.Duration
				if duration < 0 || duration > 86400 {
					duration = 0
				}
				if e = a.save(Track{ID: id, Title: title, Artist: artist, Thumbnail: thumbnail, Duration: duration}); e != nil {
					fail(e)
					return
				}
				a.mu.Lock()
				a.bridgeEvent++
				a.mu.Unlock()
				reply(map[string]any{"ok": true, "message": "Трек додано до Say Music"})
				return
			}
		} else if playlist == "" {
			fail(errors.New("Посилання не містить треку або плейлиста"))
			return
		}
		if a.config().OfflineOnly {
			fail(errors.New("Для імпорту цього посилання вимкни офлайн-режим"))
			return
		}
		if a.bridgeQueue == nil {
			fail(errors.New("Черга розширення не готова"))
			return
		}
		job := Job{ID: randomID(), Status: "running", Message: "Посилання з браузера: очікує імпорту…"}
		a.mu.Lock()
		copy := job
		a.jobs[job.ID] = &copy
		a.bridgeLastJob = job.ID
		a.mu.Unlock()
		select {
		case a.bridgeQueue <- BridgeTask{job.ID, target, strings.TrimSpace(m.Title)}:
			reply(map[string]any{"ok": true, "queued": true, "job": job.ID, "message": "Надіслано до Say Music. Імпорт виконується у програмі"})
		default:
			a.mu.Lock()
			delete(a.jobs, job.ID)
			a.mu.Unlock()
			fail(errors.New("Черга переповнена. Зачекай завершення імпорту"))
		}
	default:
		fail(errors.New("Невідома команда розширення"))
	}
}
func (a *App) bridgeWorker() {
	for {
		select {
		case <-a.life.Done():
			return
		case task := <-a.bridgeQueue:
			select {
			case a.imports <- struct{}{}:
			case <-a.life.Done():
				return
			}
			a.mu.Lock()
			j := a.jobs[task.JobID]
			j.Message = "Імпорт із браузера…"
			a.mu.Unlock()
			u, _ := url.Parse(task.URL)
			list := u.Query().Get("list")
			pid := int64(0)
			var e error
			if list == "LM" {
				pid = 1
				if !a.config().Cookies {
					e = errors.New("Спочатку підключи акаунт кнопкою у розширенні")
				}
			} else if list != "" {
				pid, e = a.externalPlaylist(list, task.Title)
			}
			if e != nil {
				a.mu.Lock()
				j := a.jobs[task.JobID]
				j.Status = "error"
				j.Message = e.Error()
				a.bridgeEvent++
				a.mu.Unlock()
				<-a.imports
				continue
			}
			// importJob releases the shared import slot on every completion path.
			a.importJob(task.JobID, task.URL, pid, a.config().Cookies, false)
			a.mu.Lock()
			a.bridgeEvent++
			a.mu.Unlock()
		}
	}
}
func (a *App) externalPlaylist(source, title string) (int64, error) {
	var id int64
	if a.db.QueryRow(`SELECT playlist_id FROM external_playlists WHERE source=?`, source).Scan(&id) == nil {
		return id, nil
	}
	if title == "" {
		title = "YouTube Music · " + source
	}
	if len(title) > 300 {
		title = title[:300]
	}
	tx, e := a.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	res, e := tx.Exec(`INSERT INTO playlists(name) VALUES(?)`, title)
	if e != nil {
		return 0, e
	}
	id, _ = res.LastInsertId()
	if _, e = tx.Exec(`INSERT INTO external_playlists VALUES(?,?)`, source, id); e != nil {
		return 0, e
	}
	return id, tx.Commit()
}
func (a *App) integrationAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/integration/status" {
		return false
	}
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return true
	}
	a.mu.Lock()
	state := map[string]any{"event": a.bridgeEvent, "authenticated": a.cfg.Cookies}
	if j := a.jobs[a.bridgeLastJob]; j != nil {
		state["job"] = *j
	}
	a.mu.Unlock()
	state["spotify"] = a.spotifyState()
	json.NewEncoder(w).Encode(state)
	return true
}

// Opens the normal installed browser, without automation or a debug profile.
func (a *App) startAuth(browser string) error {
	if a.config().OfflineOnly {
		return errors.New("Вимкни офлайн-режим")
	}
	switch browser {
	case "edge", "chrome", "brave":
	default:
		return errors.New("Обери Chrome, Edge або Brave")
	}
	a.cancelAuth()
	path, e := browserExecutable(browser)
	if e != nil {
		return e
	}
	if e = launchBrowser(path, "https://music.youtube.com/"); e != nil {
		return e
	}
	if e = a.mutateConfig(func(c *Config) { c.Browser = browser }); e != nil {
		return e
	}
	a.mu.Lock()
	a.authStatus = AuthStatus{Status: "running", Browser: browser, Message: "Увійди в YouTube Music, відкрий розширення Say Music та натисни «Підключити YouTube-акаунт»"}
	a.mu.Unlock()
	return nil
}
