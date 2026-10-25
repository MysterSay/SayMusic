package player

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed Install-Dependencies.ps1
var dependencyInstaller []byte

const extensionID = "pfapcgdhfglkpbibnidhiagdbpndcein"

func extensionRoot() string {
	legacy := filepath.Join(installRoot(), "extension")
	if info, e := os.Stat(filepath.Join(legacy, "manifest.json")); e == nil && !info.IsDir() {
		return legacy
	}
	return filepath.Join(filepath.Dir(installRoot()), "extension")
}
func installRoot() string { exe, _ := os.Executable(); return filepath.Dir(exe) }
func bridgeManifest(helper string) []byte {
	b, _ := json.MarshalIndent(map[string]any{"name": "com.saymusic.desktop", "description": "Say Music browser integration", "path": helper, "type": "stdio", "allowed_origins": []string{"chrome-extension://" + extensionID + "/"}}, "", "  ")
	return b
}
func (a *App) installBrowserBridge() error {
	helper := filepath.Join(installRoot(), "SayMusicBridge.exe")
	if stat, e := os.Stat(helper); e != nil || stat.IsDir() {
		return errors.New("Поруч із SayMusic.exe немає SayMusicBridge.exe. Розпакуй весь архів програми в одну папку")
	}
	path := filepath.Join(a.dir, "com.saymusic.desktop.json")
	if e := atomicWrite(path, bridgeManifest(helper)); e != nil {
		return e
	}
	return registerBridge(path)
}
func helpURL(step, browser, project string) (string, error) {
	pages := map[string]string{"permissions": "https://myaccount.google.com/connections"}
	if step == "extensions" {
		p := map[string]string{"chrome": "chrome://extensions/", "edge": "edge://extensions/", "brave": "brave://extensions/"}[browser]
		if p == "" {
			return "", errors.New("Обери браузер")
		}
		return p, nil
	}
	if step == "youtube-test" {
		return "https://music.youtube.com/?say_music_setup=1", nil
	}
	if step == "spotify-target" {
		if _, _, e := spotifyLink(project); e != nil {
			return "", e
		}
		return project, nil
	}

	if step == "spotify" {
		return "https://open.spotify.com/collection/tracks", nil
	}
	if step == "youtube" {
		return "https://music.youtube.com/", nil
	}
	link, ok := pages[step]
	if !ok {
		return "", errors.New("Невідомий крок інструкції")
	}
	return link, nil
}
func (a *App) setupAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, "setup/") {
		return false
	}
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	fail := func(e error) { w.WriteHeader(400); reply(map[string]string{"error": e.Error()}) }
	if path == "setup/status" {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return true
		}
		browser := r.URL.Query().Get("browser")
		direct := a.pairState()
		a.mu.Lock()
		seen := a.bridgeSeen
		job := a.jobs[a.setupJob]
		state := map[string]any{"seen": !seen.IsZero(), "seenAt": seen, "registered": bridgeRegistered(filepath.Join(a.dir, "com.saymusic.desktop.json"), browser), "extensionDir": extensionRoot(), "extensionId": extensionID}
		if job != nil {
			state["job"] = *job
		}
		state["directReady"] = direct["paired"] == true && direct["listening"] == true
		a.mu.Unlock()
		reply(state)
		return true
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return true
	}
	var b struct {
		Browser string `json:"browser"`
		Step    string `json:"step"`
		Project string `json:"project"`
	}
	if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
		fail(e)
		return true
	}
	switch path {
	case "setup/bridge":
		if e := a.installBrowserBridge(); e != nil {
			fail(e)
		} else {
			reply(true)
		}
	case "setup/unbridge":
		if e := unregisterBridge(filepath.Join(a.dir, "com.saymusic.desktop.json")); e != nil {
			fail(e)
		} else {
			reply(true)
		}
	case "setup/open":
		link, e := helpURL(b.Step, b.Browser, strings.TrimSpace(b.Project))
		if e != nil {
			fail(e)
			break
		}
		browser, e := browserExecutable(b.Browser)
		if e == nil {
			e = launchBrowser(browser, link)
		}
		if e != nil {
			fail(e)
		} else {
			reply(true)
		}
	case "setup/folder":
		if e := revealFolder(extensionRoot()); e != nil {
			fail(e)
		} else {
			reply(true)
		}
	case "setup/components":
		if e := a.installComponents(); e != nil {
			fail(e)
		} else {
			reply(true)
		}
	default:
		http.NotFound(w, r)
	}
	return true
}

type tailOutput struct{ buf []byte }

func (t *tailOutput) Write(b []byte) (int, error) {
	n := len(b)
	t.buf = append(t.buf, b...)
	if len(t.buf) > 6000 {
		t.buf = t.buf[len(t.buf)-6000:]
	}
	return n, nil
}
func (a *App) installComponents() error {
	a.mu.Lock()
	if j := a.jobs[a.setupJob]; j != nil && j.Status == "running" {
		a.mu.Unlock()
		return errors.New("Встановлення вже виконується")
	}
	id := randomID()
	a.jobs[id] = &Job{ID: id, Status: "running", Message: "Завантаження yt-dlp, Deno та FFmpeg… Це може зайняти кілька хвилин"}
	a.setupJob = id
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(a.life, 15*time.Minute)
		defer cancel()
		var output tailOutput
		e := runDependencyInstaller(ctx, installRoot(), &output)
		a.mu.Lock()
		defer a.mu.Unlock()
		j := a.jobs[id]
		if e != nil {
			j.Status = "error"
			j.Message = fmt.Sprintf("Не вдалося встановити компоненти: %v\n%s", e, strings.TrimSpace(string(output.buf)))
		} else {
			j.Status = "done"
			j.Message = "Компоненти встановлено. Можна відтворювати треки"
		}
	}()
	return nil
}

// Extract only for the installation process; downloaded tools belong beside the EXE.
func runDependencyInstaller(ctx context.Context, root string, output *tailOutput) error {
	dir, err := os.MkdirTemp("", "SayMusic-installer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "Install-Dependencies.ps1")
	if err := os.WriteFile(script, dependencyInstaller, 0600); err != nil {
		return err
	}
	c := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-InstallDirectory", root)
	quiet(c)
	c.Dir = root
	c.Stdout = output
	c.Stderr = output
	return c.Run()
}
