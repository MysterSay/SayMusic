package player

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TrackIssue struct {
	ID      string `json:"id"`
	TrackID string `json:"trackId"`
	Source  string `json:"source"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	URL     string `json:"url"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason"`
	Updated int64  `json:"updated"`
}
type ImportGap struct {
	Position int    `json:"position"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Reason   string `json:"reason"`
}

func issueKey(source string, position int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", source, position)))
	return "import:" + hex.EncodeToString(digest[:12])
}
func metadataID(title, artist string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(title)) + "\x00" + strings.ToLower(strings.TrimSpace(artist))))
	return "sm_" + hex.EncodeToString(digest[:11])
}
func writeIssue(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, i TrackIssue) error {
	if len(i.Reason) > 1200 {
		i.Reason = string([]rune(i.Reason)[:min(500, len([]rune(i.Reason)))])
	}
	_, e := tx.Exec(`INSERT INTO track_issues VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET track_id=excluded.track_id,source=excluded.source,title=excluded.title,artist=excluded.artist,url=excluded.url,kind=excluded.kind,reason=excluded.reason,updated=excluded.updated`, i.ID, i.TrackID, i.Source, i.Title, i.Artist, i.URL, i.Kind, i.Reason, time.Now().Unix())
	return e
}
func (a *App) recordDownloadResult(id string, err error) {
	if errors.Is(err, context.Canceled) || (a.life != nil && a.life.Err() != nil) {
		return
	}
	if err == nil {
		a.db.Exec(`DELETE FROM track_issues WHERE track_id=?`, id)
		return
	}
	t, e := a.track(id)
	if e != nil {
		return
	}
	kind := "download_error"
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unavailable") || strings.Contains(message, "private video") || strings.Contains(message, "removed") || strings.Contains(message, "copyright") || strings.Contains(message, "not available") {
		kind = "unavailable"
	}
	if strings.Contains(message, "no results") || strings.Contains(message, "not found") || strings.Contains(message, "не знайден") || strings.Contains(message, "no video") || strings.Contains(message, "empty playlist") {
		kind = "not_found"
	}
	source := "youtube"
	if strings.HasPrefix(id, "sp_") || strings.HasPrefix(id, "sm_") {
		source = "spotify"
	}
	a.db.Exec(`DELETE FROM track_issues WHERE track_id=? AND id<>?`, id, "track:"+id)
	writeIssue(a.db, TrackIssue{ID: "track:" + id, TrackID: id, Source: source, Title: t.Title, Artist: t.Artist, URL: t.URL, Kind: kind, Reason: err.Error()})
}
func (a *App) trashTrack(id string) error {
	if _, e := a.track(id); e != nil {
		return errors.New("Трек відсутній у бібліотеці")
	}
	_, e := a.db.Exec(`INSERT OR IGNORE INTO trash_tracks(track_id,deleted) VALUES(?,?)`, id, time.Now().Unix())
	return e
}
func (a *App) recoveryAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, "recovery/") {
		return false
	}
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	fail := func(e error) { w.WriteHeader(400); reply(map[string]string{"error": e.Error()}) }
	if path == "recovery/list" && r.Method == "GET" {
		if r.URL.Query().Get("kind") == "trash" {
			rows, e := a.db.Query(`SELECT t.id,t.title,t.artist,t.url,t.thumbnail,t.duration,tr.deleted FROM tracks t JOIN trash_tracks tr ON tr.track_id=t.id ORDER BY tr.deleted DESC,t.id`)
			if e != nil {
				fail(e)
				return true
			}
			defer rows.Close()
			out := []map[string]any{}
			for rows.Next() {
				var t Track
				var deleted int64
				if e := rows.Scan(&t.ID, &t.Title, &t.Artist, &t.URL, &t.Thumbnail, &t.Duration, &deleted); e != nil {
					fail(e)
					return true
				}
				out = append(out, map[string]any{"track": t, "deleted": deleted})
			}
			if e = rows.Err(); e != nil {
				fail(e)
			} else {
				reply(out)
			}
		} else {
			rows, e := a.db.Query(`SELECT id,track_id,source,title,artist,url,kind,reason,updated FROM track_issues WHERE track_id='' OR track_id NOT IN(SELECT track_id FROM trash_tracks) ORDER BY updated DESC,id`)
			if e != nil {
				fail(e)
				return true
			}
			defer rows.Close()
			out := []TrackIssue{}
			for rows.Next() {
				var i TrackIssue
				if e = rows.Scan(&i.ID, &i.TrackID, &i.Source, &i.Title, &i.Artist, &i.URL, &i.Kind, &i.Reason, &i.Updated); e != nil {
					fail(e)
					return true
				}
				out = append(out, i)
			}
			if e = rows.Err(); e != nil {
				fail(e)
			} else {
				reply(out)
			}
		}
		return true
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return true
	}
	var b struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		fail(errors.New("Некоректний запит"))
		return true
	}
	switch path {
	case "recovery/restore":
		res, e := a.db.Exec(`DELETE FROM trash_tracks WHERE track_id=?`, b.ID)
		if e != nil {
			fail(e)
			break
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(errors.New("Трек відсутній у кошику"))
		} else {
			reply(true)
		}
	case "recovery/purge":
		_, e := a.db.Exec(`DELETE FROM tracks WHERE id=? AND id IN(SELECT track_id FROM trash_tracks)`, b.ID)
		if e != nil {
			fail(e)
		} else {
			a.db.Exec(`DELETE FROM track_issues WHERE track_id=?`, b.ID)
			reply(true)
		}
	case "recovery/empty":
		tx, e := a.db.Begin()
		if e != nil {
			fail(e)
			break
		}
		defer tx.Rollback()
		if _, e = tx.Exec(`DELETE FROM track_issues WHERE track_id IN(SELECT track_id FROM trash_tracks)`); e == nil {
			_, e = tx.Exec(`DELETE FROM tracks WHERE id IN(SELECT track_id FROM trash_tracks)`)
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(e)
		} else {
			reply(true)
		}
	case "recovery/dismiss":
		_, e := a.db.Exec(`DELETE FROM track_issues WHERE id=?`, b.ID)
		if e != nil {
			fail(e)
		} else {
			reply(true)
		}
	case "recovery/retry":
		var id, source, raw string
		e := a.db.QueryRow(`SELECT track_id,source,url FROM track_issues WHERE id=?`, b.ID).Scan(&id, &source, &raw)
		if e != nil {
			fail(errors.New("Запис уже видалено"))
			break
		}
		if a.config().OfflineOnly {
			fail(errors.New("Вимкни офлайн-режим"))
			break
		}
		if id != "" {
			if _, e = a.startDownload(id, false); e != nil {
				fail(e)
			} else {
				reply(map[string]any{"retrying": true})
			}
			break
		}
		if source == "youtube" {
			target, e := youtubeInput(raw)
			if e != nil {
				fail(e)
				break
			}
			u, _ := url.Parse(target)
			pid := int64(0)
			if list := u.Query().Get("list"); list != "" {
				if list == "LM" {
					pid = 1
				} else {
					pid, e = a.externalPlaylist(list, "")
				}
			}
			if e != nil {
				fail(e)
				break
			}
			select {
			case a.imports <- struct{}{}:
			default:
				fail(errors.New("Інший імпорт уже виконується"))
				return true
			}
			job := Job{ID: randomID(), Status: "running", Message: "Повторний імпорт YouTube…"}
			a.mu.Lock()
			a.jobs[job.ID] = &job
			a.bridgeLastJob = job.ID
			a.mu.Unlock()
			go func() {
				a.importJob(job.ID, target, pid, a.config().Cookies, false)
				a.mu.Lock()
				done := a.jobs[job.ID].Status == "done"
				a.bridgeEvent++
				a.mu.Unlock()
				if done {
					a.db.Exec(`DELETE FROM track_issues WHERE id=?`, b.ID)
				}
			}()
			reply(map[string]any{"retrying": true})
			break
		}
		if source != "spotify" || !a.config().SpotifyConnected {
			fail(errors.New("Для цієї позиції повтори імпорт джерела"))
			break
		}
		if _, _, e = spotifyLink(raw); e != nil {
			fail(e)
			break
		}
		if _, e = a.startSpotifySync(raw); e != nil {
			fail(e)
		} else {
			reply(map[string]any{"retrying": true})
		}

	default:
		http.NotFound(w, r)
	}
	return true
}
