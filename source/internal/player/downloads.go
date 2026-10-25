package player

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Download struct {
	PersistMu           sync.Mutex
	Ctx                 context.Context
	ID, Dir, Path, MIME string
	Done                chan struct{}
	Cancel              context.CancelFunc
	Err                 error
	Manual              bool
	Size                int64
}

func (a *App) config() Config { a.mu.Lock(); defer a.mu.Unlock(); return a.cfg }
func (a *App) mutateConfig(fn func(*Config)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.cfg
	fn(&c)
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	if e = atomicWrite(filepath.Join(a.dir, "settings.json"), b); e != nil {
		return e
	}
	a.cfg = c
	return nil
}
func (a *App) updateConfig(c Config) error {
	e := a.mutateConfig(func(x *Config) {
		x.CacheEnabled = c.CacheEnabled
		x.CacheDir = c.CacheDir
		x.OfflineOnly = c.OfflineOnly
		x.PrefetchSeconds = c.PrefetchSeconds
		x.EarlyBuffering = c.EarlyBuffering
	})
	if e != nil {
		return e
	}
	if c.OfflineOnly {
		a.mu.Lock()
		a.prefetchID = ""
		if a.authCancel != nil {
			a.authCancel()
		}
		for _, d := range a.downloads {
			if !downloadDone(d) {
				d.Cancel()
			}
		}
		a.mu.Unlock()
	}
	return nil
}
func atomicWrite(path string, b []byte) error {
	tmp := path + ".new"
	if e := os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return replaceFile(tmp, path)
}
func writableDir(path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(path, ".say-write-test-")
	if e != nil {
		return fmt.Errorf("Папка недоступна для запису: %w", e)
	}
	name := f.Name()
	e = f.Close()
	_ = os.Remove(name)
	return e
}
func (a *App) initDownloads() error {
	if a.life == nil {
		a.life, a.stop = context.WithCancel(context.Background())
	}
	a.downloads = map[string]*Download{}
	a.downloadSlots = make(chan struct{}, 2)
	d, e := os.MkdirTemp(a.dir, "Buffer-")
	a.temporaryDir = d
	return e
}
func (a *App) cleanupDownloads() {
	if a.stop != nil {
		a.stop()
	}
	a.mu.Lock()
	if a.authCancel != nil {
		a.authCancel()
	}
	ds := []*Download{}
	for _, d := range a.downloads {
		d.Cancel()
		ds = append(ds, d)
	}
	a.mu.Unlock()
	deadline := time.After(4 * time.Second)
	for _, d := range ds {
		select {
		case <-d.Done:
		case <-deadline:
			return
		}
	}
	_ = os.RemoveAll(a.temporaryDir)
}
func (a *App) cachedPath(id string) (string, string, bool) {
	var p, m string
	var size int64
	e := a.db.QueryRow(`SELECT path,size,mime FROM cache_files WHERE track_id=? AND track_id NOT IN(SELECT track_id FROM trash_tracks)`, id).Scan(&p, &size, &m)
	if e != nil {
		return "", "", false
	}
	s, e := os.Stat(p)
	if e != nil || !s.Mode().IsRegular() || s.Size() != size || size <= 0 {
		_, _ = a.db.Exec(`DELETE FROM cache_files WHERE track_id=?`, id)
		return "", "", false
	}
	return p, m, true
}
func (a *App) markCached(ts []Track) []Track {
	for i := range ts {
		_, _, ts[i].Cached = a.cachedPath(ts[i].ID)
	}
	return ts
}
func (a *App) localSearch(q string) ([]Track, error) {
	rows, e := a.db.Query(`SELECT id,title,artist,url,thumbnail,duration FROM active_tracks ORDER BY added DESC`)
	if e != nil {
		return nil, e
	}
	ts, e := scanTracks(rows)
	if e != nil {
		return nil, e
	}
	out := []Track{}
	q = strings.ToLower(strings.TrimSpace(q))
	for _, t := range ts {
		if strings.Contains(strings.ToLower(t.Title+" "+t.Artist+" "+t.URL), q) {
			out = append(out, t)
		}
	}
	return out, nil
}
func (a *App) startDownload(id string, manual bool) (*Download, error) {
	if !validTrackID.MatchString(id) {
		return nil, errors.New("Некоректний ID")
	}
	if _, e := a.track(id); e != nil {
		return nil, errors.New("Трек відсутній у бібліотеці")
	}
	if p, m, ok := a.cachedPath(id); ok {
		a.recordDownloadResult(id, nil)
		done := make(chan struct{})
		close(done)
		return &Download{ID: id, Path: p, MIME: m, Done: done, Cancel: func() {}}, nil
	}
	c := a.config()
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.downloads[id]; d != nil && (d.Ctx == nil || d.Ctx.Err() == nil || downloadDone(d)) {
		select {
		case <-d.Done:
			if d.Err == nil {
				if _, e := os.Stat(d.Path); e == nil {
					if manual {
						d.Manual = true
					}
					return d, nil
				}
			}
			delete(a.downloads, id)
		default:
			if manual {
				d.Manual = true
			}
			return d, nil
		}
	}
	if c.OfflineOnly {
		return nil, errors.New("Цей трек не збережений для офлайн-прослуховування")
	}
	if a.downloads == nil || a.downloadSlots == nil {
		return nil, errors.New("Буфер відтворення не ініціалізовано")
	}
	root := a.temporaryDir
	if c.CacheEnabled || manual {
		root = c.CacheDir
		if root == "" {
			root = filepath.Join(a.dir, "AudioCache")
		}
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp(root, ".say-pending-"+id+"-")
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(a.life, 45*time.Minute)
	d := &Download{Ctx: ctx, ID: id, Dir: dir, Done: make(chan struct{}), Cancel: cancel, Manual: manual}
	a.downloads[id] = d
	go a.download(ctx, d, root, c.CacheEnabled || manual)
	return d, nil
}
func (a *App) download(ctx context.Context, d *Download, root string, persistent bool) {
	var err error
	defer func() {
		a.recordDownloadResult(d.ID, err)
		d.Cancel()
		if err != nil {
			_ = os.RemoveAll(d.Dir)
		}
		a.mu.Lock()
		d.Err = err
		close(d.Done)
		a.mu.Unlock()
	}()
	select {
	case a.downloadSlots <- struct{}{}:
		defer func() { <-a.downloadSlots }()
	case <-ctx.Done():
		err = ctx.Err()
		return
	}
	output := filepath.Join(d.Dir, "audio.%(ext)s")
	args := []string{"--ignore-config", "--no-warnings", "--no-progress", "--no-playlist", "--socket-timeout", "20", "--retries", "2", "--fragment-retries", "2", "-f", "bestaudio[ext=webm]/bestaudio[ext=m4a]/bestaudio/best", "-o", output, "--", a.audioTarget(d.ID)}
	cmd := exec.CommandContext(ctx, tool("yt-dlp"), args...)
	quiet(cmd)
	outputLog, e := cmd.CombinedOutput()
	if e != nil {
		detail := strings.TrimSpace(string(outputLog))
		if len(detail) > 1200 {
			detail = detail[len(detail)-1200:]
		}
		err = fmt.Errorf("yt-dlp: %w: %s", e, detail)
		log.Printf("Download %s: %v", d.ID, err)
		return
	}
	path, mt := findAudio(d.Dir, false)
	if path == "" {
		err = errors.New("Не знайдено завантажений аудіофайл")
		return
	}
	f, e := os.Open(path)
	if e != nil {
		err = e
		return
	}
	header := make([]byte, 16)
	n, _ := f.Read(header)
	f.Close()
	if !validAudioHeader(header[:n]) {
		err = errors.New("YouTube повернув некоректні медіадані. Перевір мережу та онови yt-dlp")
		return
	}
	stat, e := os.Stat(path)
	if e != nil {
		err = e
		return
	}
	a.mu.Lock()
	persistent = persistent || d.Manual
	a.mu.Unlock()
	if persistent {
		if root == a.temporaryDir {
			root = a.config().CacheDir
			if root == "" {
				root = filepath.Join(a.dir, "AudioCache")
			}
		}
		dest, e := a.persistFile(d.ID, path, root, mt, stat.Size())
		if e != nil {
			err = e
			return
		}
		path = dest
		_ = os.RemoveAll(d.Dir)
	}

	a.mu.Lock()
	d.Path = path
	d.MIME = mt
	d.Size = stat.Size()
	a.mu.Unlock()
}
func validAudioHeader(b []byte) bool {
	return len(b) >= 4 && (string(b[:4]) == "\x1aE\xdf\xa3" || string(b[:4]) == "OggS" || string(b[:4]) == "fLaC" || string(b[:4]) == "RIFF" || string(b[:3]) == "ID3" || (b[0] == 0xff && b[1]&0xe0 == 0xe0) || (len(b) >= 8 && string(b[4:8]) == "ftyp"))
}
func audioMIME(p string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSuffix(p, ".part"))) {
	case ".webm":
		return "audio/webm"
	case ".m4a", ".mp4":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	}
	m := mime.TypeByExtension(filepath.Ext(p))
	if m == "" {
		m = "application/octet-stream"
	}
	return m
}
func findAudio(dir string, partial bool) (string, string) {
	files, _ := filepath.Glob(filepath.Join(dir, "audio.*"))
	for _, p := range files {
		ext := strings.ToLower(filepath.Ext(p))
		if strings.Contains(p, "-Frag") || ext == ".ytdl" || ext == ".json" || ext == ".temp" {
			continue
		}
		if ext == ".part" && !partial {
			continue
		}
		if st, e := os.Stat(p); e == nil && st.Size() > 0 {
			return p, audioMIME(p)
		}
	}
	return "", ""
}
func (a *App) media(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !validTrackID.MatchString(id) {
		http.Error(w, "Invalid ID", 400)
		return
	}
	if p, mt, ok := a.cachedPath(id); ok {
		w.Header().Set("Content-Type", mt)
		http.ServeFile(w, r, p)
		return
	}
	d, e := a.startDownload(id, false)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	select {
	case <-d.Done:
	case <-r.Context().Done():
		return
	}
	a.mu.Lock()
	p, mt, err := d.Path, d.MIME, d.Err
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	w.Header().Set("Content-Type", mt)
	http.ServeFile(w, r, p)
}

// Streams the same growing raw file being cached; playback and offline caching
// never download a second copy of the current track or transcode it.
func (a *App) streamRaw(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !validTrackID.MatchString(id) {
		http.Error(w, "Invalid ID", 400)
		return
	}
	if p, mt, ok := a.cachedPath(id); ok {
		w.Header().Set("Content-Type", mt)
		http.ServeFile(w, r, p)
		return
	}
	d, e := a.startDownload(id, false)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" && rangeHeader != "bytes=0-" {
		a.media(w, r)
		return
	}
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	var file *os.File
	defer func() {
		if file != nil {
			file.Close()
		}
	}()
	buf := make([]byte, 64<<10)
	sent := false
	var position int64
	for {
		a.mu.Lock()
		p, mt, err := d.Path, d.MIME, d.Err
		done := false
		select {
		case <-d.Done:
			done = true
		default:
		}
		a.mu.Unlock()
		if done && err != nil {
			if !sent {
				http.Error(w, err.Error(), 502)
			}
			return
		}
		if done && !sent {
			w.Header().Set("Content-Type", mt)
			http.ServeFile(w, r, p)
			return
		}
		if file == nil {
			if p == "" {
				p, mt = findAudio(d.Dir, true)
			}
			if p != "" {
				f, err := openShared(p)
				if err == nil {
					file = f
					w.Header().Set("Content-Type", mt)
					w.Header().Set("Accept-Ranges", "none")
				}
			}
		}
		if file != nil {
			n, readErr := file.Read(buf)
			if n > 0 {
				if !sent && n < 16 && !done {
					_, _ = file.Seek(-int64(n), io.SeekCurrent)
					select {
					case <-ticker.C:
					case <-r.Context().Done():
						return
					}
					continue
				}
				if !sent && !validAudioHeader(buf[:n]) {
					if !sent {
						http.Error(w, "Некоректні медіадані", 502)
					}
					return
				}
				sent = true
				position += int64(n)
				if _, e = w.Write(buf[:n]); e != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				continue
			}
			if readErr != nil && readErr != io.EOF {
				return
			}
			if done {
				return
			}
		}
		// A Windows rename may replace the open .part path. Reopen the completed file
		// at exactly the byte offset already sent; Unix may retain the original FD.
		if done && p != "" && file == nil {
			var err error
			file, err = openShared(p)
			if err != nil {
				return
			}
			_, _ = file.Seek(position, io.SeekStart)
			continue
		}
		select {
		case <-ticker.C:
		case <-r.Context().Done():
			return
		}
	}
}
func (a *App) setCurrent(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentID = id
	for key, d := range a.downloads {
		if key != id && key != a.prefetchID && !d.Manual {
			select {
			case <-d.Done:
				if strings.HasPrefix(filepath.Clean(d.Dir), filepath.Clean(a.temporaryDir)+string(os.PathSeparator)) {
					_ = os.RemoveAll(d.Dir)
					delete(a.downloads, key)
				}
			default:
				d.Cancel()
			}
		}
	}
}
func (a *App) prefetch(id string) (bool, error) {
	c := a.config()
	if c.OfflineOnly {
		return false, nil
	}
	a.mu.Lock()
	current := a.currentID
	if old := a.prefetchID; old != "" && old != id && old != current {
		if d := a.downloads[old]; d != nil && !d.Manual {
			d.Cancel()
		}
	}
	a.prefetchID = ""
	a.mu.Unlock()
	if c.EarlyBuffering != nil && !*c.EarlyBuffering {
		return false, nil
	}
	if id == "" {
		return false, nil
	}
	if _, _, ok := a.cachedPath(id); ok {
		return true, nil
	}
	if _, e := a.startDownload(id, false); e != nil {
		return false, e
	}
	a.mu.Lock()
	a.prefetchID = id
	a.mu.Unlock()
	return true, nil
}
func (a *App) extraAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, "cache/") && !strings.HasPrefix(path, "auth/") && path != "playback" {
		return false
	}
	fail := func(e error) { w.WriteHeader(400); json.NewEncoder(w).Encode(map[string]string{"error": e.Error()}) }
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	if path == "auth/status" {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return true
		}
		a.mu.Lock()
		s := a.authStatus
		a.mu.Unlock()
		reply(s)
		return true
	}
	if path == "cache/status" {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return true
		}
		rows, e := a.db.Query(`SELECT track_id FROM cache_files`)
		if e != nil {
			fail(e)
			return true
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		valid := []string{}
		for _, id := range ids {
			if _, _, ok := a.cachedPath(id); ok {
				valid = append(valid, id)
			}
		}
		a.mu.Lock()
		pending := []string{}
		for id, d := range a.downloads {
			select {
			case <-d.Done:
			default:
				pending = append(pending, id)
			}
		}
		a.mu.Unlock()
		reply(map[string]any{"cached": valid, "downloading": pending})
		return true
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return true
	}
	var b struct {
		ID      string   `json:"id"`
		IDs     []string `json:"ids"`
		Browser string   `json:"browser"`
	}
	if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
		fail(e)
		return true
	}
	switch path {
	case "auth/start":
		if e := a.startAuth(b.Browser); e != nil {
			fail(e)
		} else {
			reply(true)
		}
	case "auth/cancel":
		a.mu.Lock()
		a.authStatus = AuthStatus{Status: "idle", Message: "Очікування підключення скасовано"}
		if a.authCancel != nil {
			a.authCancel()
		}
		a.mu.Unlock()
		reply(true)
	case "cache/folder":
		p, e := chooseFolder(a.config().Language)
		if e != nil {
			fail(e)
		} else {
			reply(map[string]string{"path": p})
		}
	case "playback":
		if !validTrackID.MatchString(b.ID) {
			fail(errors.New("Некоректний трек"))
			break
		}
		a.setCurrent(b.ID)
		d, e := a.startDownload(b.ID, false)
		if e != nil {
			fail(e)
			break
		}
		a.mu.Lock()
		ready := false
		select {
		case <-d.Done:
			ready = d.Err == nil
		default:
		}
		a.mu.Unlock()
		reply(map[string]any{"ready": ready})
	case "cache/prefetch":
		ok, e := a.prefetch(b.ID)
		if e != nil {
			fail(e)
		} else {
			reply(map[string]bool{"scheduled": ok})
		}
	case "cache/add":
		if len(b.IDs) == 0 || len(b.IDs) > 10000 {
			fail(errors.New("Обери від 1 до 10000 треків"))
			break
		}
		if a.config().OfflineOnly {
			fail(errors.New("Для завантаження вимкни офлайн-режим"))
			break
		}
		job := Job{ID: randomID(), Status: "running", Message: "Збереження для офлайн-прослуховування…"}
		a.mu.Lock()
		copy := job
		a.jobs[job.ID] = &copy
		a.mu.Unlock()
		go a.cacheBatch(job.ID, b.IDs)
		reply(job)
	case "cache/remove":
		if !validTrackID.MatchString(b.ID) {
			fail(errors.New("Некоректний ID"))
			break
		}
		p, _, ok := a.cachedPath(b.ID)
		if ok {
			if e := os.Remove(p); e != nil {
				fail(e)
				break
			}
			_, e := a.db.Exec(`DELETE FROM cache_files WHERE track_id=?`, b.ID)
			if e != nil {
				fail(e)
				break
			}
		}
		reply(true)
	default:
		http.NotFound(w, r)
	}
	return true
}
func (a *App) cacheBatch(jobID string, ids []string) {
	count := 0
	failed := 0
	for _, id := range ids {
		if a.config().OfflineOnly {
			break
		}
		d, e := a.startDownload(id, true)
		if e == nil {
			select {
			case <-d.Done:
				a.mu.Lock()
				e = d.Err
				a.mu.Unlock()
			case <-a.life.Done():
				return
			}
		}
		if e == nil {
			e = a.ensurePersistent(d)
		}
		if e != nil {
			failed++
		} else {
			count++
		}
		a.mu.Lock()
		j := a.jobs[jobID]
		j.Count = count
		j.Message = fmt.Sprintf("Збережено %d / %d; помилок: %d", count, len(ids), failed)
		a.mu.Unlock()
	}
	a.mu.Lock()
	j := a.jobs[jobID]
	j.Count = count
	j.Status = "done"
	j.Message = fmt.Sprintf("Офлайн: збережено %d / %d; помилок: %d", count, len(ids), failed)
	a.mu.Unlock()
}

func downloadDone(d *Download) bool {
	select {
	case <-d.Done:
		return true
	default:
		return false
	}
}
func (a *App) persistFile(id, source, root, mt string, size int64) (string, error) {
	if e := writableDir(root); e != nil {
		return "", e
	}
	dest := filepath.Join(root, id+strings.ToLower(filepath.Ext(source)))
	input, e := openShared(source)
	if e != nil {
		return "", e
	}
	defer input.Close()
	output, e := os.CreateTemp(root, ".say-complete-")
	if e != nil {
		return "", e
	}
	temp := output.Name()
	defer os.Remove(temp)
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	for _, err := range []error{copyErr, syncErr, closeErr} {
		if err != nil {
			return "", err
		}
	}
	if e = replaceFile(temp, dest); e != nil {
		return "", e
	}
	_, e = a.db.Exec(`INSERT INTO cache_files VALUES(?,?,?,?,?) ON CONFLICT(track_id) DO UPDATE SET path=excluded.path,size=excluded.size,mime=excluded.mime,created=excluded.created`, id, dest, size, mt, time.Now().Unix())
	if e != nil {
		_ = os.Remove(dest)
		return "", e
	}
	return dest, nil
}
func (a *App) ensurePersistent(d *Download) error {
	d.PersistMu.Lock()
	defer d.PersistMu.Unlock()
	if _, _, ok := a.cachedPath(d.ID); ok {
		return nil
	}
	c := a.config()
	root := c.CacheDir
	if root == "" {
		root = filepath.Join(a.dir, "AudioCache")
	}
	a.mu.Lock()
	p, mt := d.Path, d.MIME
	a.mu.Unlock()
	st, e := os.Stat(p)
	if e != nil {
		return e
	}
	dest, e := a.persistFile(d.ID, p, root, mt, st.Size())
	if e != nil {
		return e
	}
	a.mu.Lock()
	d.Path = dest
	d.MIME = mt
	d.Size = st.Size()
	a.mu.Unlock()
	return nil
}

func (a *App) requestQuit() {
	a.quitOnce.Do(func() {
		if a.quit != nil {
			close(a.quit)
		}
	})
}

// Feed the growing cache into a streaming container. WebView2 can start decoding
// MP3 immediately without seeking to the final WebM cues or MP4 metadata.
func (a *App) stream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !validTrackID.MatchString(id) {
		http.Error(w, "Invalid ID", 400)
		return
	}
	if p, mt, ok := a.cachedPath(id); ok {
		w.Header().Set("Content-Type", mt)
		http.ServeFile(w, r, p)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	input, writer := io.Pipe()
	defer input.Close()
	req := r.Clone(ctx)
	req.Header = r.Header.Clone()
	req.Header.Del("Range")
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, tool("ffmpeg"), "-hide_banner", "-loglevel", "error", "-probesize", "2048", "-analyzeduration", "0", "-i", "pipe:0", "-vn", "-codec:a", "libmp3lame", "-b:a", "192k", "-f", "mp3", "-flush_packets", "1", "pipe:1")
	quiet(cmd)
	cmd.Stdin = input
	cmd.Stderr = &stderr
	output, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err = cmd.Start(); err != nil {
		http.Error(w, "FFmpeg: "+err.Error(), 503)
		return
	}
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		a.streamRaw(&streamInput{writer: writer, header: make(http.Header)}, req)
		_ = writer.Close()
	}()
	buffer := make([]byte, 16<<10)
	sent := false
	for {
		n, readErr := output.Read(buffer)
		if n > 0 {
			if !sent {
				w.Header().Set("Content-Type", "audio/mpeg")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Accept-Ranges", "none")
				sent = true
			}
			if _, err = w.Write(buffer[:n]); err != nil {
				break
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
	cancel()
	_ = input.Close()
	_ = cmd.Wait()
	<-feedDone
	if !sent {
		log.Printf("Streaming %s failed: %s", id, stderr.String())
		http.Error(w, "Не вдалося запустити аудіопотік", 502)
	}
}

type streamInput struct {
	writer *io.PipeWriter
	header http.Header
	status int
}

func (s *streamInput) Header() http.Header { return s.header }
func (s *streamInput) WriteHeader(code int) {
	s.status = code
	if code >= 400 {
		_ = s.writer.CloseWithError(fmt.Errorf("audio source HTTP %d", code))
	}
}
func (s *streamInput) Write(b []byte) (int, error) {
	if s.status >= 400 {
		return 0, io.ErrClosedPipe
	}
	return s.writer.Write(b)
}
func (s *streamInput) Flush() {}
