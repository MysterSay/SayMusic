package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var spotifyID = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)

type SpotifyTrack struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Artist      string  `json:"artist"`
	Thumbnail   string  `json:"thumbnail"`
	Duration    float64 `json:"duration"`
	Position    int     `json:"position"`
	Unavailable bool    `json:"unavailable"`
}

func spotifyLink(raw string) (kind, id string, err error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host != "open.spotify.com" || u.User != nil {
		return "", "", errors.New("Некоректне посилання Spotify")
	}
	path := strings.Trim(u.Path, "/")
	// Spotify may include an intl-xx locale prefix.
	parts := strings.Split(path, "/")
	if len(parts) == 3 && strings.HasPrefix(parts[0], "intl-") {
		parts = parts[1:]
		path = strings.Join(parts, "/")
	}
	if path == "collection/tracks" {
		return "likes", "", nil
	}
	if len(parts) != 2 || !oneOf(parts[0], "track", "playlist", "album") || !spotifyID.MatchString(parts[1]) {
		return "", "", errors.New("Відкрий пісню або плейлист Spotify")
	}
	return parts[0], parts[1], nil
}
func (a *App) audioTarget(id string) string {
	if strings.HasPrefix(id, "sp_") || strings.HasPrefix(id, "sm_") {
		t, e := a.track(id)
		if e != nil {
			return ""
		}
		return "ytsearch1:" + t.Artist + " - " + t.Title + " official audio"
	}
	return "https://www.youtube.com/watch?v=" + id
}
func (a *App) youtubeSeed(ctx context.Context, id string) (string, error) {
	if validID.MatchString(id) {
		return id, nil
	}
	if !validTrackID.MatchString(id) {
		return "", errors.New("Некоректний трек")
	}
	ts, e := a.extract(ctx, a.audioTarget(id), 1, false)
	if e != nil {
		return "", e
	}
	if len(ts) == 0 {
		return "", errors.New("Не знайдено відповідного аудіо на YouTube")
	}
	return ts[0].ID, nil
}
func (a *App) spotifyState() map[string]any {
	state := map[string]any{"connected": a.config().SpotifyConnected, "direct": a.spotifyDirect()}
	var raw string
	if a.db.QueryRow(`SELECT value FROM state WHERE key='spotify-sync'`).Scan(&raw) == nil {
		var v map[string]any
		if json.Unmarshal([]byte(raw), &v) == nil {
			for k, x := range v {
				state[k] = x
			}
		}
	}
	return state
}
func (a *App) spotifySetState(request, status, message string) error {
	raw, _ := json.Marshal(map[string]any{"request": request, "status": status, "message": message, "time": time.Now().Unix()})
	_, e := a.db.Exec(`INSERT OR REPLACE INTO state(key,value) VALUES('spotify-sync',?)`, string(raw))
	return e
}
func (a *App) spotifyAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, "spotify/") {
		return false
	}
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	if path == "spotify/status" && r.Method == "GET" {
		reply(a.spotifyState())
		return true
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return true
	}
	switch path {
	case "spotify/sync":
		var b struct {
			URL string `json:"url"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if b.URL == "" {
			b.URL = "https://open.spotify.com/collection/tracks"
		}
		if _, e := a.startSpotifySync(b.URL); e != nil {
			w.WriteHeader(400)
			reply(map[string]string{"error": e.Error()})
		} else {
			reply(a.spotifyState())
		}

	case "spotify/cancel":
		a.spotifyMu.Lock()
		if a.spotifyCancel != nil {
			a.spotifyCancel()
		}
		a.spotifyMu.Unlock()
		reply(true)
	case "spotify/disconnect":
		a.disconnectSpotify()
		if e := a.mutateConfig(func(c *Config) { c.SpotifyConnected = false }); e != nil {
			w.WriteHeader(400)
			reply(map[string]string{"error": e.Error()})
		} else {
			a.db.Exec(`DELETE FROM state WHERE key='spotify-sync'`)
			reply(true)
		}
	default:
		http.NotFound(w, r)
	}
	return true
}

// Only metadata is imported. Audio is found by title/artist on YouTube when played.
func (a *App) importSpotify(raw, title string, tracks []SpotifyTrack, complete bool) (int64, error) {
	return a.importSpotifyReport(raw, title, tracks, complete, nil)
}
func (a *App) importSpotifyReport(raw, title string, tracks []SpotifyTrack, complete bool, gaps []ImportGap) (int64, error) {
	return a.importSpotifyReportContext(context.Background(), raw, title, tracks, complete, gaps)
}
func (a *App) importSpotifyReportContext(ctx context.Context, raw, title string, tracks []SpotifyTrack, complete bool, gaps []ImportGap) (int64, error) {
	if a.config().OfflineOnly {
		return 0, errors.New("Вимкни офлайн-режим")
	}
	kind, sourceID, e := spotifyLink(raw)
	if e != nil {
		return 0, e
	}
	if len(tracks) > 10000 || (kind != "track" && !complete && len(gaps) == 0) || (kind == "track" && (len(tracks) != 1 || tracks[0].ID != sourceID)) {
		return 0, errors.New("Список Spotify не зібрано повністю. Повтори синхронізацію")
	}
	for _, t := range tracks {
		if (t.ID != "" && !spotifyID.MatchString(t.ID)) || strings.TrimSpace(t.Title) == "" || len(t.Title) > 1000 || len(t.Artist) > 1000 || t.Duration < 0 || t.Duration > 86400 {
			return 0, errors.New("Некоректні метадані Spotify")
		}
	}
	if len(gaps) > 10000 {
		return 0, errors.New("Завеликий звіт пропущених пісень")
	}
	for _, g := range gaps {
		if g.Position < 1 || g.Position > 100000 || len(g.Title) > 1000 || len(g.Artist) > 1000 || len(g.Reason) > 1200 {
			return 0, errors.New("Некоректний звіт пропущених пісень")
		}
	}
	tx, e := a.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	pid := int64(0)
	if kind != "track" {
		source := "spotify:" + kind + ":" + sourceID
		if kind == "likes" {
			source = "spotify:likes"
		}
		if e = tx.QueryRow(`SELECT playlist_id FROM external_playlists WHERE source=?`, source).Scan(&pid); e != nil {
			title = strings.TrimSpace(title)
			if title == "" {
				title = "Spotify · " + sourceID
			}
			if len(title) > 300 {
				title = string([]rune(title)[:min(150, len([]rune(title)))])
			}
			res, e := tx.Exec(`INSERT INTO playlists(name) VALUES(?)`, title)
			if e != nil {
				return 0, e
			}
			pid, _ = res.LastInsertId()
			if _, e = tx.Exec(`INSERT INTO external_playlists(source,playlist_id) VALUES(?,?)`, source, pid); e != nil {
				return 0, e
			}
		}
		if complete {
			if _, e = tx.Exec(`DELETE FROM playlist_tracks WHERE playlist_id=?`, pid); e != nil {
				return 0, e
			}
		}
	}
	var offset int
	if !complete && pid != 0 {
		tx.QueryRow(`SELECT COALESCE(MAX(position),-1)+1 FROM playlist_tracks WHERE playlist_id=?`, pid).Scan(&offset)
	}
	_, e = tx.Exec(`DELETE FROM track_issues WHERE source='spotify' AND url=? AND kind IN ('import_gap','source_unavailable','import_error')`, raw)
	if e != nil {
		return 0, e
	}
	upsert, e := tx.Prepare(`INSERT INTO tracks VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,artist=excluded.artist,url=excluded.url,thumbnail=excluded.thumbnail,duration=excluded.duration`)
	if e != nil {
		return 0, e
	}
	defer upsert.Close()
	membership, e := tx.Prepare(`INSERT OR IGNORE INTO playlist_tracks VALUES(?,?,?)`)
	if e != nil {
		return 0, e
	}
	defer membership.Close()
	fallbackIDs := map[string]bool{}
	rows, e := tx.Query(`SELECT id FROM tracks WHERE id LIKE 'sm_%'`)
	if e != nil {
		return 0, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return 0, e
		}
		fallbackIDs[id] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	for i, t := range tracks {
		if e = ctx.Err(); e != nil {
			return 0, e
		}
		thumb := ""
		if u, e := url.Parse(t.Thumbnail); e == nil && u.Scheme == "https" && oneOf(u.Host, "i.scdn.co", "mosaic.scdn.co") {
			thumb = u.String()
		}
		id := "sp_" + t.ID
		trackURL := "https://open.spotify.com/track/" + t.ID
		if t.ID == "" {
			id = metadataID(t.Title, t.Artist)
			trackURL = raw
		} else {
			fallback := metadataID(t.Title, t.Artist)
			if fallbackIDs[fallback] {
				id = fallback
			}
		}
		_, e = upsert.Exec(id, t.Title, t.Artist, trackURL, thumb, t.Duration, time.Now().Unix())
		if e != nil {
			return 0, e
		}
		if t.ID == "" {
			fallbackIDs[id] = true
		}
		if t.Unavailable || t.ID == "" {
			reason := "Spotify не надав посилання; аудіо шукається за назвою"
			if t.Unavailable {
				reason = "Пісня недоступна у Spotify; аудіо можна пошукати на YouTube"
			}
			if e = writeIssue(tx, TrackIssue{ID: issueKey(raw, t.Position), TrackID: id, Source: "spotify", Title: t.Title, Artist: t.Artist, URL: raw, Kind: "source_unavailable", Reason: reason}); e != nil {
				return 0, e
			}
		}
		if pid != 0 {
			if _, e = membership.Exec(pid, id, offset+i); e != nil {
				return 0, e
			}
		}
	}
	for _, g := range gaps {
		title := g.Title
		if title == "" {
			title = fmt.Sprintf("Пісня №%d", g.Position)
		}
		reason := g.Reason
		if reason == "" {
			reason = "Не вдалося прочитати цю позицію Spotify"
		}
		if e = writeIssue(tx, TrackIssue{ID: issueKey(raw, g.Position), Source: "spotify", Title: title, Artist: g.Artist, URL: raw, Kind: "import_gap", Reason: reason}); e != nil {
			return 0, e
		}
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	a.mu.Lock()
	a.bridgeEvent++
	a.mu.Unlock()
	return pid, nil
}
func (a *App) spotifyBridge(action, raw, title, request string, tracks []SpotifyTrack, complete bool, gaps ...[]ImportGap) (map[string]any, error) {
	switch action {
	case "spotify/fetch":
		job, e := a.startSpotifySync(raw)
		return map[string]any{"ok": e == nil, "job": job, "message": "Spotify: імпорт запущено у плеєрі"}, e
	case "spotify/status":
		state := a.spotifyState()
		state["ok"] = true
		return state, nil
	case "spotify/claim":
		res, e := a.db.Exec(`UPDATE state SET value=json_set(value,'$.status','collecting','$.message','Spotify: збирання списку…') WHERE key='spotify-sync' AND json_extract(value,'$.request')=? AND json_extract(value,'$.status')='waiting'`, request)
		if e != nil {
			return nil, e
		}
		n, _ := res.RowsAffected()
		return map[string]any{"ok": true, "claimed": n == 1}, nil
	case "spotify/auth":
		return nil, errors.New("Онови розширення та скопіюй сесію Spotify повторно")
	case "spotify/error":
		if request != "" && a.spotifyState()["request"] != request {
			return map[string]any{"ok": true}, nil
		}
		if request != "" {
			a.spotifySetState(request, "error", title)
		}
		if _, _, e := spotifyLink(raw); e == nil && !strings.Contains(strings.ToLower(title), "скасовано") {
			writeIssue(a.db, TrackIssue{ID: issueKey("spotify:"+raw, 0), Source: "spotify", Title: "Імпорт Spotify", URL: raw, Kind: "import_error", Reason: title})
		}
		return map[string]any{"ok": true}, nil
	case "spotify/import":
		if request != "" && a.spotifyState()["request"] != request {
			return nil, errors.New("Цю синхронізацію вже скасовано")
		}
		var missing []ImportGap
		if len(gaps) > 0 {
			missing = gaps[0]
		}
		pid, e := a.importSpotifyReport(raw, title, tracks, complete, missing)
		if e != nil {
			return nil, e
		}
		message := fmt.Sprintf("Spotify: додано треків — %d", len(tracks))
		if len(missing) > 0 {
			message += fmt.Sprintf("; не прочитано — %d (Додатково → Проблемні пісні)", len(missing))
		}
		if request != "" {
			a.spotifySetState(request, "done", message)
		}
		return map[string]any{"ok": true, "playlist": pid, "message": message}, nil
	}
	return nil, errors.New("Невідома команда Spotify")
}
