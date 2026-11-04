package player

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type storageLocation struct {
	Root     string `json:"root"`
	Previous string `json:"previous,omitempty"`
}

func locationFile() string { return filepath.Join(installRoot(), "storage-path.json") }
func readLocation() storageLocation {
	var loc storageLocation
	b, _ := os.ReadFile(locationFile())
	_ = json.Unmarshal(b, &loc)
	if loc.Root != "" {
		return loc
	}
	legacy, _ := os.UserConfigDir()
	old := filepath.Join(legacy, "SayMusic")
	if _, err := os.Stat(old); os.IsNotExist(err) {
		if _, err = os.Stat(filepath.Join(legacy, "AuroraMusic", "library.db")); err == nil {
			old = filepath.Join(legacy, "AuroraMusic")
		}
	}
	var cfg Config
	b, _ = os.ReadFile(filepath.Join(old, "settings.json"))
	_ = json.Unmarshal(b, &cfg)
	loc.Root = cfg.CacheDir
	if loc.Root == "" {
		loc.Root = filepath.Join(installRoot(), "SayMusicData")
	}
	rel, relErr := filepath.Rel(old, loc.Root)
	if relErr == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))) {
		loc.Root = filepath.Join(installRoot(), "SayMusicData")
	}
	loc.Previous = old
	return loc
}

// DataDirectory resolves the same location for the UI, CLI and native messaging.
func DataDirectory() string {
	loc := readLocation()
	if loc.Previous != "" {
		if _, e := os.Stat(filepath.Join(loc.Previous, "runtime.json")); e == nil {
			return loc.Previous
		}
	}
	return filepath.Join(loc.Root, "data")
}
func prepareStorage() (string, error) {
	loc := readLocation()
	loc.Root = filepath.Clean(loc.Root)
	if !filepath.IsAbs(loc.Root) {
		return "", fmt.Errorf("Потрібен абсолютний шлях до сховища")
	}
	data := filepath.Join(loc.Root, "data")
	music := filepath.Join(loc.Root, "music")
	if e := writableDir(data); e != nil {
		return "", e
	}
	if e := writableDir(music); e != nil {
		return "", e
	}
	// Record the destination before moving anything, so an interrupted migration resumes.
	intent, _ := json.Marshal(loc)
	if e := atomicWrite(locationFile(), intent); e != nil {
		return "", e
	}
	log.Printf("Startup: storage destination=%s previous=%s", data, loc.Previous)
	if loc.Previous != "" && !samePath(loc.Previous, data) {
		if e := moveStorageTree(loc.Previous, data); e != nil {
			return "", e
		}
	}
	// Organize only recognizable audio files; unrelated user files stay untouched.
	for _, root := range []string{loc.Root, filepath.Join(data, "AudioCache")} {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if strings.Contains("|.mp3|.m4a|.webm|.opus|.ogg|.flac|.wav|", "|"+ext+"|") {
				if e := moveStorageTree(filepath.Join(root, entry.Name()), filepath.Join(music, entry.Name())); e != nil {
					return "", e
				}
			}
		}
	}
	loc.Previous = ""
	b, _ := json.Marshal(loc)
	if e := atomicWrite(locationFile(), b); e != nil {
		return "", e
	}
	return data, nil
}
func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }
func moveStorageTree(src, dst string) error {
	if samePath(src, dst) {
		return nil
	}
	st, e := os.Stat(src)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if st.IsDir() {
		if e = os.MkdirAll(dst, 0700); e != nil {
			return e
		}
		es, e := os.ReadDir(src)
		if e != nil {
			return e
		}
		for _, x := range es {
			if e = moveStorageTree(filepath.Join(src, x.Name()), filepath.Join(dst, x.Name())); e != nil {
				return e
			}
		}
		return os.Remove(src)
	}
	if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
		return e
	}
	if _, e = os.Stat(dst); e == nil {
		a, ea := hashFile(src)
		b, eb := hashFile(dst)
		if ea != nil || eb != nil || !bytes.Equal(a, b) {
			return fmt.Errorf("Конфлікт перенесення: %s; оригінал збережено", dst)
		}
		return os.Remove(src)
	}
	if e = os.Rename(src, dst); e == nil {
		return nil
	}
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	h := sha256.New()
	_, e = io.Copy(io.MultiWriter(out, h), in)
	if e == nil {
		e = out.Sync()
	}
	closeErr := out.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		os.Remove(dst)
		return e
	}
	b, e := hashFile(dst)
	if e != nil || !bytes.Equal(h.Sum(nil), b) {
		os.Remove(dst)
		return fmt.Errorf("Помилка перевірки перенесення %s", src)
	}
	in.Close()
	return os.Remove(src)
}
func hashBytes(b []byte) []byte { s := sha256.Sum256(b); return s[:] }
func (a *App) requestStorageChange(root string) error {
	current := filepath.Dir(a.dir)
	if samePath(root, current) {
		return nil
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("Потрібен абсолютний шлях")
	}
	// Do not move into a descendant of the current data directory.
	rel, relErr := filepath.Rel(a.dir, root)
	if relErr == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))) {
		return fmt.Errorf("Обери папку поза поточним каталогом data")
	}
	if e := writableDir(root); e != nil {
		return e
	}
	loc := storageLocation{Root: root, Previous: a.dir}
	b, _ := json.Marshal(loc)
	if e := atomicWrite(locationFile(), b); e != nil {
		return e
	}
	go func() { time.Sleep(time.Second); a.requestQuit() }()
	return nil
}
func (a *App) musicDir() string { return filepath.Join(a.config().CacheDir, "music") }
func (a *App) repairCacheLocations() error {
	rows, e := a.db.Query("SELECT track_id,path FROM cache_files")
	if e != nil {
		return e
	}
	type item struct{ id, path string }
	var items []item
	for rows.Next() {
		var i item
		if e = rows.Scan(&i.id, &i.path); e != nil {
			rows.Close()
			return e
		}
		items = append(items, i)
	}
	rows.Close()
	for _, i := range items {
		dst := filepath.Join(a.musicDir(), filepath.Base(i.path))
		if !samePath(i.path, dst) {
			if e = moveStorageTree(i.path, dst); e != nil {
				log.Printf("Cache organization deferred for %s: %v", i.id, e)
				continue
			}
			if _, e = os.Stat(dst); e == nil {
				if _, e = a.db.Exec("UPDATE cache_files SET path=? WHERE track_id=?", dst, i.id); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func hashFile(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return nil, e
	}
	return h.Sum(nil), nil
}
