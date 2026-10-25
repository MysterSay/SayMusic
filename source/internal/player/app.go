package player

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	_ "modernc.org/sqlite"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"saymusic/internal/ui"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var assets = ui.Assets

type Track struct {
	PlaylistTitle string  `json:"-"`
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Artist        string  `json:"artist"`
	URL           string  `json:"url"`
	Thumbnail     string  `json:"thumbnail"`
	Duration      float64 `json:"duration"`
	Cached        bool    `json:"cached"`
}
type Config struct {
	Language         string       `json:"language"`
	InterfaceTheme   string       `json:"interfaceTheme"`
	InterfaceAccent  string       `json:"interfaceAccent"`
	SpotifyConnected bool         `json:"spotifyConnected"`
	InterfaceColor   string       `json:"interfaceColor"`
	Island           IslandConfig `json:"island"`
	Recommendations  bool         `json:"recommendations"`
	Browser          string       `json:"browser"`
	Cookies          bool         `json:"cookies"`
	CacheEnabled     bool         `json:"cacheEnabled"`
	CacheDir         string       `json:"cacheDir"`
	OfflineOnly      bool         `json:"offlineOnly"`
	EarlyBuffering   *bool        `json:"earlyBuffering"`
	PrefetchSeconds  int          `json:"prefetchSeconds"`
}
type Job struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}
type App struct {
	spotifyMu        sync.Mutex
	spotifyRunMu     sync.Mutex
	spotifyEpoch     uint64
	spotifyCancel    context.CancelFunc
	spotifyCollect   func(context.Context, string, []browserCookie, func(string)) (spotifyReport, error)
	spotifyClear     func()
	desktop          func(string) error
	player           PlayerState
	commands         []PlayerCommand
	commandSeq       uint64
	pairMu           sync.Mutex
	pairKey          string
	pairServer       *http.Server
	pairError        string
	pairReplay       map[string]time.Time
	sealPrivate      func([]byte) ([]byte, error)
	openPrivate      func([]byte) ([]byte, error)
	bridgeSeen       time.Time
	setupJob         string
	bridgeQueue      chan BridgeTask
	bridgeEvent      uint64
	bridgeLastJob    string
	quit             chan struct{}
	quitOnce         sync.Once
	lastSeen         atomic.Int64
	db               *sql.DB
	dir, token       string
	mu               sync.Mutex
	cfg              Config
	jobs             map[string]*Job
	imports          chan struct{}
	streams          chan struct{}
	authMu           sync.Mutex
	downloads        map[string]*Download
	downloadSlots    chan struct{}
	temporaryDir     string
	prefetchID       string
	currentID        string
	embeddedOpen     func(string) error
	embeddedStatuses map[string]EmbeddedAuthStatus
	authStatus       AuthStatus
	authCancel       context.CancelFunc
	life             context.Context
	stop             context.CancelFunc
}

var validTrackID = regexp.MustCompile(`^(?:[A-Za-z0-9_-]{11}|sp_[A-Za-z0-9]{22}|sm_[a-f0-9]{22})$`)
var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

func randomID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Run() {
	runtime.LockOSThread()
	if !initProcessGroup() {
		return
	}
	defer func() {
		if e := recover(); e != nil {
			log.Printf("Fatal: %v", e)
			showFatal(fmt.Sprint(e))
		}
	}()
	dir, e := os.UserConfigDir()
	must(e)
	baseDir := dir
	dir = filepath.Join(baseDir, "SayMusic")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err = os.Stat(filepath.Join(baseDir, "AuroraMusic", "library.db")); err == nil {
			must(os.Rename(filepath.Join(baseDir, "AuroraMusic"), dir))
		}
	}
	must(os.MkdirAll(dir, 0700))
	f, e := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	must(e)
	defer f.Close()
	log.SetOutput(f)
	db, e := sql.Open("sqlite", filepath.Join(dir, "library.db"))
	must(e)
	defer db.Close()
	db.SetMaxOpenConns(1)
	a := &App{db: db, dir: dir, token: randomID(), jobs: map[string]*Job{}, imports: make(chan struct{}, 1), streams: make(chan struct{}, 2)}
	must(a.schema())
	_ = os.Remove(filepath.Join(dir, "oauth.dat"))
	_, _ = a.db.Exec(`UPDATE playlists SET name='Уподобані YouTube (імпорт)' WHERE name='Уподобані YouTube · OAuth' AND id IN (SELECT playlist_id FROM external_playlists WHERE source='oauth:likes'); DELETE FROM external_playlists WHERE source='oauth:likes';`)
	a.quit = make(chan struct{})
	a.life, a.stop = context.WithCancel(context.Background())
	defer a.stop()
	must(a.initDownloads())
	defer a.cleanupDownloads()
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	_ = json.Unmarshal(b, &a.cfg)
	a.cfg.Language = normalizeLanguage(a.cfg.Language)
	a.cfg.Island = normalizeIsland(a.cfg.Island)
	if a.cfg.PrefetchSeconds == 0 {
		a.cfg.PrefetchSeconds = 45
	}
	if a.cfg.CacheDir == "" {
		a.cfg.CacheDir = filepath.Join(a.dir, "AudioCache")
	}
	a.lastSeen.Store(time.Now().Unix())
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if time.Now().Unix()-a.lastSeen.Load() > 180 {
				a.requestQuit()
			}
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", a.api)
	mux.HandleFunc("/stream", a.stream)
	mux.HandleFunc("/media", a.media)
	mux.HandleFunc("/bridge", a.bridge)
	sub, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	must(e)
	address := "http://" + listener.Addr().String()
	must(a.initBridge(address))
	defer a.cleanupBridge()
	a.initPairBridge()
	defer a.closePairBridge()
	link := address + "/?token=" + a.token
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https://i.ytimg.com https://lh3.googleusercontent.com https://yt3.ggpht.com https://i.scdn.co https://mosaic.scdn.co data: blob:; media-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		if r.Host != listener.Addr().String() {
			http.Error(w, "Invalid host", 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/stream" || r.URL.Path == "/media" || r.URL.Path == "/bridge" {
			if r.URL.Query().Get("token") != a.token {
				http.Error(w, "Unauthorized", 401)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != address {
				http.Error(w, "Invalid origin", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	defer server.Close()
	go func() {
		if e := server.Serve(listener); e != nil && e != http.ErrServerClosed {
			log.Print(e)
		}
	}()
	fmt.Println("Say Music:", link)
	if openWindow(link, a.dir, a.quit, a) {
		return
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	select {
	case <-quit:
	case <-a.quit:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func (a *App) schema() error {
	_, e := a.db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS tracks(id TEXT PRIMARY KEY,title TEXT NOT NULL,artist TEXT NOT NULL,url TEXT NOT NULL,thumbnail TEXT NOT NULL,duration REAL NOT NULL,added INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS favorites(track_id TEXT PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,added INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS playlists(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL,system INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS playlist_tracks(playlist_id INTEGER REFERENCES playlists(id) ON DELETE CASCADE,track_id TEXT REFERENCES tracks(id) ON DELETE CASCADE,position INTEGER NOT NULL,PRIMARY KEY(playlist_id,track_id));
CREATE TABLE IF NOT EXISTS external_playlists(source TEXT PRIMARY KEY,playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS searches(query TEXT PRIMARY KEY,ids TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS cache_files(track_id TEXT PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,path TEXT NOT NULL,size INTEGER NOT NULL,mime TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS listening_history(track_id TEXT PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,plays INTEGER NOT NULL DEFAULT 0,last_played INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS trash_tracks(track_id TEXT PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,deleted INTEGER NOT NULL);
CREATE VIEW IF NOT EXISTS active_tracks AS SELECT * FROM tracks WHERE id NOT IN(SELECT track_id FROM trash_tracks);
CREATE TABLE IF NOT EXISTS track_issues(id TEXT PRIMARY KEY,track_id TEXT NOT NULL,source TEXT NOT NULL,title TEXT NOT NULL,artist TEXT NOT NULL,url TEXT NOT NULL,kind TEXT NOT NULL,reason TEXT NOT NULL,updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS state(key TEXT PRIMARY KEY,value TEXT NOT NULL);
INSERT OR IGNORE INTO playlists(id,name,system) VALUES(1,'Уподобані YouTube Music',1);
INSERT INTO playlists(name,system) SELECT 'Уподобані Spotify',1 WHERE NOT EXISTS(SELECT 1 FROM external_playlists WHERE source='spotify:likes');
INSERT OR IGNORE INTO external_playlists SELECT 'spotify:likes',id FROM playlists WHERE name='Уподобані Spotify' AND system=1 LIMIT 1;`)
	return e
}
func (a *App) save(t Track) error {
	if !validTrackID.MatchString(t.ID) {
		return errors.New("Некоректний ID треку")
	}
	if strings.HasPrefix(t.ID, "sm_") {
		if _, _, e := spotifyLink(t.URL); e != nil {
			return e
		}
	} else if strings.HasPrefix(t.ID, "sp_") {
		t.URL = "https://open.spotify.com/track/" + strings.TrimPrefix(t.ID, "sp_")
	} else {
		t.URL = "https://www.youtube.com/watch?v=" + t.ID
	}
	_, e := a.db.Exec(`INSERT INTO tracks VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,artist=excluded.artist,thumbnail=excluded.thumbnail,duration=excluded.duration`, t.ID, t.Title, t.Artist, t.URL, t.Thumbnail, t.Duration, time.Now().Unix())
	return e
}
func scanTracks(rows *sql.Rows) ([]Track, error) {
	defer rows.Close()
	out := []Track{}
	for rows.Next() {
		var t Track
		if e := rows.Scan(&t.ID, &t.Title, &t.Artist, &t.URL, &t.Thumbnail, &t.Duration); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (a *App) track(id string) (Track, error) {
	var t Track
	e := a.db.QueryRow(`SELECT id,title,artist,url,thumbnail,duration FROM active_tracks WHERE id=?`, id).Scan(&t.ID, &t.Title, &t.Artist, &t.URL, &t.Thumbnail, &t.Duration)
	return t, e
}
func tool(name string) string {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	exe, _ := os.Executable()
	for _, p := range []string{filepath.Join(filepath.Dir(exe), "tools", name+suffix), filepath.Join(filepath.Dir(exe), name+suffix)} {
		if s, e := os.Stat(p); e == nil && !s.IsDir() {
			return p
		}
	}
	p, e := exec.LookPath(name)
	if e == nil {
		return p
	}
	return name + suffix
}
func (a *App) authArgs() []string {
	a.mu.Lock()
	c := a.cfg
	a.mu.Unlock()
	if c.Cookies {
		return []string{"--cookies", filepath.Join(a.dir, "cookies.txt")}
	}
	if c.Browser != "" {
		return nil
	}
	return nil
}
func (a *App) extract(ctx context.Context, target string, limit int, auth bool) ([]Track, error) {
	args := []string{"--ignore-config", "--no-warnings", "--skip-download", "--flat-playlist", "--dump-json", "--ignore-errors", "--socket-timeout", "20", "--retries", "2"}
	if limit > 0 {
		args = append(args, "--playlist-end", strconv.Itoa(limit))
	}
	if auth {
		args = append(args, a.authArgs()...)
	}
	args = append(args, "--", target)
	// Serialize authenticated extraction because cookie jars may be rewritten by yt-dlp.
	if auth {
		a.authMu.Lock()
		defer a.authMu.Unlock()
	}
	cmd := exec.CommandContext(ctx, tool("yt-dlp"), args...)
	quiet(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	tracks := []Track{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var x struct {
			PlaylistTitle string  `json:"playlist_title"`
			ID            string  `json:"id"`
			Title         string  `json:"title"`
			Uploader      string  `json:"uploader"`
			Channel       string  `json:"channel"`
			Duration      float64 `json:"duration"`
			Cached        bool    `json:"cached"`
			Thumbnail     string  `json:"thumbnail"`
		}
		if err := dec.Decode(&x); err != nil {
			break
		}
		if !validID.MatchString(x.ID) {
			if x.Title != "" {
				writeIssue(a.db, TrackIssue{ID: issueKey("youtube:"+target+":"+x.Title, 0), Source: "youtube", Title: x.Title, Artist: x.Uploader, URL: target, Kind: "import_gap", Reason: "Джерело не надало доступний ID для цієї пісні"})
			}
			continue
		}
		if oneOf(strings.ToLower(strings.Trim(x.Title, "[]")), "deleted video", "private video", "unavailable video") {
			writeIssue(a.db, TrackIssue{ID: "track:" + x.ID, TrackID: x.ID, Source: "youtube", Title: x.Title, Artist: x.Uploader, URL: target, Kind: "unavailable", Reason: "YouTube позначив цю пісню як видалену, приватну або недоступну"})
		}
		artist := x.Uploader
		if artist == "" {
			artist = x.Channel
		}
		thumb := x.Thumbnail
		if thumb == "" {
			thumb = "https://i.ytimg.com/vi/" + x.ID + "/mqdefault.jpg"
		}
		tracks = append(tracks, Track{PlaylistTitle: x.PlaylistTitle, ID: x.ID, Title: x.Title, Artist: artist, URL: "https://www.youtube.com/watch?v=" + x.ID, Thumbnail: thumb, Duration: x.Duration})
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(tracks) == 0 {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 1400 {
			msg = msg[len(msg)-1400:]
		}
		if msg == "" {
			msg = "yt-dlp не повернув треків"
		}
		if e != nil {
			msg = e.Error() + ": " + msg
		}
		return nil, errors.New(msg)
	}
	if e != nil {
		log.Printf("Partial extraction: %v %s", e, stderr.String())
	}
	return tracks, nil
}
func youtubeInput(s string) (string, error) {
	u, e := url.Parse(s)
	if e != nil {
		return "", e
	}
	h := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("Потрібне посилання YouTube")
	}
	switch h {
	case "youtube.com", "www.youtube.com", "music.youtube.com", "m.youtube.com", "youtu.be":
	default:
		return "", errors.New("Підтримуються лише посилання YouTube")
	}
	return u.String(), nil
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	fail := func(e error) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	a.lastSeen.Store(time.Now().Unix())
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	if a.embeddedAuthAPI(w, r, path) || a.recoveryAPI(w, r, path) || a.spotifyAPI(w, r, path) || a.experienceAPI(w, r, path) || a.pairAPI(w, r, path) || a.setupAPI(w, r, path) {
		return
	}
	if a.integrationAPI(w, r) {
		return
	}
	if a.extraAPI(w, r, path) {
		return
	}
	var body struct {
		Query           string          `json:"query"`
		Name            string          `json:"name"`
		ID              string          `json:"id"`
		Playlist        int64           `json:"playlist"`
		IDs             []string        `json:"ids"`
		Browser         string          `json:"browser"`
		Cookies         bool            `json:"cookies"`
		Value           json.RawMessage `json:"value"`
		Refresh         bool            `json:"refresh"`
		Favorite        bool            `json:"favorite"`
		CacheEnabled    bool            `json:"cacheEnabled"`
		CacheDir        string          `json:"cacheDir"`
		OfflineOnly     bool            `json:"offlineOnly"`
		EarlyBuffering  *bool           `json:"earlyBuffering"`
		PrefetchSeconds int             `json:"prefetchSeconds"`
	}
	if r.Method != "GET" && r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.Method == "POST" && r.URL.Path != "/api/cookies" {
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			fail(e)
			return
		}
	}

	mut := map[string]bool{"search": true, "import": true, "radio": true, "playlist/create": true, "playlist/rename": true, "playlist/delete": true, "playlist/add": true, "playlist/remove": true, "playlist/reorder": true, "favorites/set": true, "track/delete": true, "settings": true, "cookies": true, "logout": true, "state": true, "shutdown": true}
	if mut[path] && r.Method != "POST" && path != "settings" && path != "state" {
		http.Error(w, "POST required", 405)
		return
	}
	switch path {
	case "heartbeat":
		reply(true)
	case "library":
		rows, e := a.db.Query(`SELECT id,title,artist,url,thumbnail,duration FROM active_tracks ORDER BY added DESC`)
		if e != nil {
			fail(e)
			return
		}
		ts, e := scanTracks(rows)
		if e != nil {
			fail(e)
			return
		}
		reply(a.markCached(ts))
	case "favorites":
		rows, e := a.db.Query(`SELECT t.id,t.title,t.artist,t.url,t.thumbnail,t.duration FROM active_tracks t JOIN favorites f ON t.id=f.track_id ORDER BY f.added DESC,f.rowid DESC`)
		if e != nil {
			fail(e)
			return
		}
		ts, e := scanTracks(rows)
		if e != nil {
			fail(e)
			return
		}
		reply(a.markCached(ts))
	case "favorites/set":
		if _, e := a.track(body.ID); e != nil {
			fail(errors.New("Трек не знайдено"))
			return
		}
		var e error
		if body.Favorite {
			_, e = a.db.Exec(`INSERT OR IGNORE INTO favorites(track_id,added) VALUES(?,?)`, body.ID, time.Now().Unix())
		} else {
			_, e = a.db.Exec(`DELETE FROM favorites WHERE track_id=?`, body.ID)
		}
		if e != nil {
			fail(e)
			return
		}
		reply(map[string]any{"id": body.ID, "favorite": body.Favorite})
	case "playlists":
		rows, e := a.db.Query(`SELECT p.id,p.name,p.system,count(pt.track_id),COALESCE((SELECT CASE WHEN ep.source LIKE 'spotify:%' THEN 'spotify' ELSE 'youtube' END FROM external_playlists ep WHERE ep.playlist_id=p.id LIMIT 1),'') FROM playlists p LEFT JOIN playlist_tracks pt ON pt.playlist_id=p.id AND pt.track_id NOT IN(SELECT track_id FROM trash_tracks) GROUP BY p.id ORDER BY p.system DESC,p.id`)
		if e != nil {
			fail(e)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, n, s int
			var name, source string
			if e = rows.Scan(&id, &name, &s, &n, &source); e != nil {
				fail(e)
				return
			}
			out = append(out, map[string]any{"id": id, "name": name, "system": s, "count": n, "source": source})
		}
		reply(out)
	case "playlist":
		id := r.URL.Query().Get("id")
		rows, e := a.db.Query(`SELECT t.id,t.title,t.artist,t.url,t.thumbnail,t.duration FROM active_tracks t JOIN playlist_tracks p ON t.id=p.track_id WHERE p.playlist_id=? ORDER BY p.position`, id)
		if e != nil {
			fail(e)
			return
		}
		ts, e := scanTracks(rows)
		if e != nil {
			fail(e)
			return
		}
		reply(a.markCached(ts))
	case "search":
		q := strings.TrimSpace(body.Query)
		if a.config().OfflineOnly {
			ts, e := a.localSearch(q)
			if e != nil {
				fail(e)
				return
			}
			reply(map[string]any{"tracks": a.markCached(ts), "cached": true})
			return
		}
		if q == "" || len(q) > 1000 {
			fail(errors.New("Введи назву або посилання"))
			return
		}
		target := "ytsearch15:" + q
		if strings.Contains(q, "://") {
			var e error
			target, e = youtubeInput(q)
			if e != nil {
				fail(e)
				return
			}
		}
		if !body.Refresh {
			var ids string
			if a.db.QueryRow(`SELECT ids FROM searches WHERE query=?`, q).Scan(&ids) == nil {
				var list []string
				_ = json.Unmarshal([]byte(ids), &list)
				out := []Track{}
				for _, id := range list {
					if t, e := a.track(id); e == nil {
						out = append(out, t)
					}
				}
				if len(out) > 0 {
					reply(map[string]any{"tracks": a.markCached(out), "cached": true})
					return
				}
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		ts, e := a.extract(ctx, target, 15, false)
		if e != nil {
			fail(e)
			return
		}
		ids := []string{}
		for _, t := range ts {
			if e = a.save(t); e != nil {
				fail(e)
				return
			}
			ids = append(ids, t.ID)
		}
		encoded, _ := json.Marshal(ids)
		_, e = a.db.Exec(`INSERT OR REPLACE INTO searches VALUES(?,?,?)`, q, string(encoded), time.Now().Unix())
		if e != nil {
			fail(e)
			return
		}
		reply(map[string]any{"tracks": a.markCached(ts), "cached": false})
	case "import", "radio":
		if path == "import" && strings.Contains(body.Query, "open.spotify.com/") {
			job, e := a.startSpotifySync(body.Query)
			if e != nil {
				fail(e)
			} else {
				reply(job)
			}
			return
		}
		if a.config().OfflineOnly {
			fail(errors.New("Увімкнено офлайн-режим. Імпорт і радіо потребують мережі"))
			return
		}
		target := ""
		auth := false
		if path == "radio" {
			if !validTrackID.MatchString(body.ID) {
				fail(errors.New("Некоректний трек"))
				return
			}
			seed, e := a.youtubeSeed(r.Context(), body.ID)
			if e != nil {
				fail(e)
				return
			}
			target = "https://www.youtube.com/watch?v=" + seed + "&list=RD" + seed
		} else {
			var e error
			target, e = youtubeInput(body.Query)
			if e != nil {
				fail(e)
				return
			}
			u, _ := url.Parse(target)
			auth = u.Query().Get("list") == "LM"
			if auth {
				body.Playlist = 1
				if len(a.authArgs()) == 0 {
					fail(errors.New("Увійди в YouTube Music у налаштуваннях"))
					return
				}
			}
		}
		if path == "import" && body.Playlist == 0 {
			u, _ := url.Parse(target)
			if list := u.Query().Get("list"); list != "" && list != "LM" {
				var e error
				body.Playlist, e = a.externalPlaylist(list, "")
				if e != nil {
					fail(e)
					return
				}
			}
		}
		select {
		case a.imports <- struct{}{}:
		default:
			fail(errors.New("Дочекайся завершення поточного імпорту"))
			return
		}
		job := Job{ID: randomID(), Status: "running", Message: "Отримання треків…"}
		a.mu.Lock()
		storedJob := job
		a.jobs[job.ID] = &storedJob
		a.mu.Unlock()
		go a.importJob(job.ID, target, body.Playlist, auth, path == "radio")
		reply(job)
	case "job":
		a.mu.Lock()
		j := a.jobs[r.URL.Query().Get("id")]
		if j == nil {
			a.mu.Unlock()
			fail(errors.New("Завдання не знайдено"))
			return
		}
		copy := *j
		a.mu.Unlock()
		reply(copy)
	case "playlist/create":
		name := strings.TrimSpace(body.Name)
		if name == "" || len(name) > 200 {
			fail(errors.New("Некоректна назва"))
			return
		}
		res, e := a.db.Exec(`INSERT INTO playlists(name) VALUES(?)`, name)
		if e != nil {
			fail(e)
			return
		}
		id, _ := res.LastInsertId()
		reply(map[string]any{"id": id})
	case "playlist/rename":
		if strings.TrimSpace(body.Name) == "" {
			fail(errors.New("Введи назву"))
			return
		}
		_, e := a.db.Exec(`UPDATE playlists SET name=? WHERE id=? AND system=0`, body.Name, body.Playlist)
		if e != nil {
			fail(e)
			return
		}
		reply(true)
	case "playlist/delete":
		_, e := a.db.Exec(`DELETE FROM playlists WHERE id=? AND system=0`, body.Playlist)
		if e != nil {
			fail(e)
			return
		}
		reply(true)
	case "playlist/add":
		if e := a.addToPlaylist(body.Playlist, body.IDs); e != nil {
			fail(e)
			return
		}
		reply(true)
	case "playlist/remove":
		_, e := a.db.Exec(`DELETE FROM playlist_tracks WHERE playlist_id=? AND track_id=?`, body.Playlist, body.ID)
		if e != nil {
			fail(e)
			return
		}
		reply(true)
	case "playlist/reorder":
		tx, e := a.db.Begin()
		if e != nil {
			fail(e)
			return
		}
		defer tx.Rollback()
		for i, id := range body.IDs {
			if _, e = tx.Exec(`UPDATE playlist_tracks SET position=? WHERE playlist_id=? AND track_id=?`, i, body.Playlist, id); e != nil {
				fail(e)
				return
			}
		}
		if e = tx.Commit(); e != nil {
			fail(e)
			return
		}
		reply(true)
	case "track/delete":
		if e := a.trashTrack(body.ID); e != nil {
			fail(e)
			return
		}
		reply(true)
	case "settings":
		if r.Method == "POST" {
			c := a.config()
			c.CacheEnabled = body.CacheEnabled
			c.OfflineOnly = body.OfflineOnly
			c.CacheDir = strings.TrimSpace(body.CacheDir)
			c.EarlyBuffering = body.EarlyBuffering
			c.PrefetchSeconds = body.PrefetchSeconds
			if c.CacheDir == "" {
				c.CacheDir = filepath.Join(a.dir, "AudioCache")
			}
			if !filepath.IsAbs(c.CacheDir) {
				fail(errors.New("Потрібен абсолютний шлях до папки кешу"))
				return
			}
			if c.PrefetchSeconds < 10 || c.PrefetchSeconds > 180 {
				c.PrefetchSeconds = 45
			}
			if c.CacheEnabled {
				if e := writableDir(c.CacheDir); e != nil {
					fail(e)
					return
				}
			}
			if e := a.updateConfig(c); e != nil {
				fail(e)
				return
			}
		}
		reply(a.config())
	case "cookies":
		b, e := io.ReadAll(r.Body)
		if e != nil {
			fail(e)
			return
		}
		if !bytes.Contains(b, []byte("Netscape HTTP Cookie File")) && !bytes.Contains(b, []byte("HTTP Cookie File")) {
			fail(errors.New("Потрібен cookies.txt у форматі Netscape"))
			return
		}
		filtered := []string{"# Netscape HTTP Cookie File"}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Split(strings.TrimPrefix(strings.TrimSpace(line), "#HttpOnly_"), "\t")
			if len(fields) == 7 {
				d := strings.TrimPrefix(fields[0], ".")
				if d == "youtube.com" || strings.HasSuffix(d, ".youtube.com") {
					filtered = append(filtered, line)
				}
			}
		}
		if len(filtered) == 1 {
			fail(errors.New("У файлі немає cookies YouTube"))
			return
		}
		a.authMu.Lock()
		e = os.WriteFile(filepath.Join(a.dir, "cookies.txt"), []byte(strings.Join(filtered, "\n")+"\n"), 0600)
		a.authMu.Unlock()
		if e != nil {
			fail(e)
			return
		}
		a.mu.Lock()
		a.cfg.Cookies = true
		c, _ := json.Marshal(a.cfg)
		e = os.WriteFile(filepath.Join(a.dir, "settings.json"), c, 0600)
		a.mu.Unlock()
		if e != nil {
			fail(e)
			return
		}
		reply(true)
	case "logout":
		a.mu.Lock()
		if a.authCancel != nil {
			a.authCancel()
		}
		a.authStatus = AuthStatus{Status: "idle"}
		a.mu.Unlock()
		a.authMu.Lock()
		_ = os.Remove(filepath.Join(a.dir, "cookies.txt"))
		a.authMu.Unlock()
		if e := a.mutateConfig(func(c *Config) { c.Cookies = false }); e != nil {
			fail(e)
			return
		}
		reply(true)
	case "diagnostics":
		out := map[string]string{}
		for _, n := range []string{"yt-dlp", "ffmpeg", "deno"} {
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			arg := "--version"
			if n == "ffmpeg" {
				arg = "-version"
			}
			cmd := exec.CommandContext(ctx, tool(n), arg)
			quiet(cmd)
			e := cmd.Run()
			cancel()
			if e != nil {
				out[n] = "Не встановлено"
			} else {
				out[n] = "Встановлено"
			}
		}
		reply(out)
	case "state":
		if r.Method == "POST" {
			if len(body.Value) > 1<<20 {
				fail(errors.New("Завеликий стан"))
				return
			}
			_, e := a.db.Exec(`INSERT OR REPLACE INTO state VALUES('player',?)`, string(body.Value))
			if e != nil {
				fail(e)
				return
			}
			reply(true)
		} else {
			var s string
			if a.db.QueryRow(`SELECT value FROM state WHERE key='player'`).Scan(&s) != nil {
				s = `{}`
			}
			w.Write([]byte(s))
		}
	case "shutdown":
		reply(true)
		go func() { time.Sleep(200 * time.Millisecond); a.requestQuit() }()
	default:
		http.NotFound(w, r)
	}
}
func (a *App) addToPlaylist(pid int64, ids []string) error {
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var pos int
	if e = tx.QueryRow(`SELECT COALESCE(MAX(position),-1)+1 FROM playlist_tracks WHERE playlist_id=?`, pid).Scan(&pos); e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = tx.Exec(`INSERT OR IGNORE INTO playlist_tracks VALUES(?,?,?)`, pid, id, pos); e != nil {
			return e
		}
		pos++
	}
	return tx.Commit()
}
func (a *App) replacePlaylist(pid int64, ids []string) error {
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`DELETE FROM playlist_tracks WHERE playlist_id=?`, pid); e != nil {
		return e
	}
	for i, id := range ids {
		if _, e = tx.Exec(`INSERT OR IGNORE INTO playlist_tracks VALUES(?,?,?)`, pid, id, i); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (a *App) importJob(id, target string, pid int64, auth, radio bool) {
	defer func() { <-a.imports }()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	limit := 0
	if radio {
		limit = 50
	}
	ts, e := a.extract(ctx, target, limit, auth)
	ids := []string{}
	if e == nil {
		for _, t := range ts {
			if e = a.save(t); e != nil {
				break
			}
			ids = append(ids, t.ID)
		}
	}
	if e == nil && pid > 0 {
		var external bool
		a.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM external_playlists WHERE playlist_id=?)`, pid).Scan(&external)
		if external && len(ts) > 0 && strings.TrimSpace(ts[0].PlaylistTitle) != "" {
			title := []rune(ts[0].PlaylistTitle)
			if len(title) > 150 {
				title = title[:150]
			}
			a.db.Exec(`UPDATE playlists SET name=? WHERE id=? AND name LIKE 'YouTube Music · %'`, string(title), pid)
		}
		if (auth && pid == 1) || external {
			e = a.replacePlaylist(pid, ids)
		} else {
			e = a.addToPlaylist(pid, ids)
		}
	}
	if e != nil {
		title := "Імпорт YouTube"
		if pid > 0 {
			a.db.QueryRow(`SELECT name FROM playlists WHERE id=?`, pid).Scan(&title)
		}
		writeIssue(a.db, TrackIssue{ID: issueKey("youtube:"+target, 0), Source: "youtube", Title: title, URL: target, Kind: "import_error", Reason: e.Error()})
	} else {
		a.db.Exec(`DELETE FROM track_issues WHERE id=?`, issueKey("youtube:"+target, 0))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	j := a.jobs[id]
	j.Count = len(ids)
	if e != nil {
		j.Status = "error"
		j.Message = e.Error()
	} else {
		j.Status = "done"
		j.Message = "Імпортовано треків: " + strconv.Itoa(len(ids))
		if radio {
			b, _ := json.Marshal(ids)
			j.Message = string(b)
		}
	}
	if len(a.jobs) > 100 {
		for k, v := range a.jobs {
			if k != id && v.Status != "running" {
				delete(a.jobs, k)
			}
		}
	}
}
func (a *App) legacyStream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !validTrackID.MatchString(id) {
		http.Error(w, "Invalid ID", 400)
		return
	}
	if _, e := a.track(id); e != nil {
		http.Error(w, "Track not in library", 404)
		return
	}
	select {
	case a.streams <- struct{}{}:
		defer func() { <-a.streams }()
	case <-r.Context().Done():
		return
	}
	start, _ := strconv.ParseFloat(r.URL.Query().Get("start"), 64)
	if start < 0 || start > 86400 {
		start = 0
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	args := []string{"--ignore-config", "--no-warnings", "--no-playlist", "--socket-timeout", "20", "--retries", "2", "-f", "bestaudio/best", "-o", "-", "--", a.audioTarget(id)}
	yt := exec.CommandContext(ctx, tool("yt-dlp"), args...)
	quiet(yt)
	var ytErr bytes.Buffer
	yt.Stderr = &ytErr
	pipe, e := yt.StdoutPipe()
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	fa := []string{"-hide_banner", "-loglevel", "error", "-i", "pipe:0"}
	if start > 0 {
		fa = append(fa, "-ss", strconv.FormatFloat(start, 'f', 2, 64))
	}
	fa = append(fa, "-vn", "-ac", "2", "-ar", "48000", "-codec:a", "libmp3lame", "-b:a", "192k", "-f", "mp3", "pipe:1")
	ff := exec.CommandContext(ctx, tool("ffmpeg"), fa...)
	quiet(ff)
	ff.Stdin = pipe
	var ffErr bytes.Buffer
	ff.Stderr = &ffErr
	audio, e := ff.StdoutPipe()
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if e = yt.Start(); e != nil {
		http.Error(w, "yt-dlp: "+e.Error(), 503)
		return
	}
	if e = ff.Start(); e != nil {
		cancel()
		_ = yt.Wait()
		http.Error(w, "FFmpeg: "+e.Error(), 503)
		return
	}
	buf := make([]byte, 16384)
	n, readErr := audio.Read(buf)
	if n == 0 {
		cancel()
		_ = ff.Wait()
		_ = yt.Wait()
		log.Printf("Playback %s: %v yt=%s ff=%s", id, readErr, ytErr.String(), ffErr.String())
		http.Error(w, "Не вдалося відтворити. Перевір залежності та app.log", 502)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Accept-Ranges", "none")
	w.Write(buf[:n])
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	_, copyErr := io.Copy(w, audio)
	cancel()
	fe := ff.Wait()
	ye := yt.Wait()
	if copyErr != nil || fe != nil || ye != nil {
		log.Printf("Stream ended %s: copy=%v ff=%v yt=%v %s %s", id, copyErr, fe, ye, ytErr.String(), ffErr.String())
	}
}
