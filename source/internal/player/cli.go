package player

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"saymusic/internal/nativehost"
	"strconv"
	"strings"
	"time"
)

const cliHelp = `SayMusic.exe --api <command> [value]
Commands:
 status | play | pause | toggle | stop | next | previous
 volume <0..100> | seek <seconds> | repeat <off|all|one>
 playlist-create "Name" | playlist-add-current <playlist-id>
 playlists | dynamic <on|off>
The player must already be running. Replies are JSON; errors return exit code 1.
`

func RunCLI(args []string) int {
	cliConsole()
	if len(args) == 0 || args[0] == "--help" || (args[0] == "--api" && len(args) == 1) {
		fmt.Print(cliHelp)
		return 0
	}
	if args[0] != "--api" {
		fmt.Fprintln(os.Stderr, "Use --api or --help")
		return 2
	}
	args = args[1:]
	cmd := args[0]
	if cmd == "help" {
		fmt.Print(cliHelp)
		return 0
	}
	endpoint, body, err := cliRequest(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var d nativehost.Descriptor
	b, err := os.ReadFile(filepath.Join(DataDirectory(), "runtime.json"))
	if err == nil {
		err = json.Unmarshal(b, &d)
	}
	u, e := url.Parse(d.URL)
	if err != nil || e != nil || u == nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || len(d.Token) != 48 {
		fmt.Fprintln(os.Stderr, "Say Music is not running or the local connection is invalid")
		return 1
	}
	query := url.Values{"token": []string{d.Token}}
	method := "GET"
	var reader io.Reader
	if body != nil {
		method = "POST"
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, d.URL+"/api/"+endpoint+"?"+query.Encode(), reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(string(raw))
	if res.StatusCode >= 400 {
		return 1
	}
	return 0
}
func cliRequest(args []string) (string, any, error) {
	cmd := args[0]
	value := ""
	if len(args) > 1 {
		value = strings.Join(args[1:], " ")
	}
	command := func(action string, v float64) (string, any, error) {
		return "player/command", map[string]any{"action": action, "value": v}, nil
	}
	switch cmd {
	case "status":
		return "player/status", nil, nil
	case "playlists":
		return "playlists", nil, nil
	case "play", "pause", "toggle", "stop", "next", "previous":
		return command(cmd, 0)
	case "volume", "seek":
		v, e := strconv.ParseFloat(value, 64)
		if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || (cmd == "volume" && v > 100) {
			return "", nil, fmt.Errorf("Invalid %s value", cmd)
		}
		if cmd == "volume" {
			v /= 100
		}
		return command(cmd, v)
	case "repeat":
		m := map[string]float64{"off": 0, "all": 1, "one": 2}
		v, ok := m[value]
		if !ok {
			return "", nil, fmt.Errorf("repeat: off|all|one")
		}
		return command("repeat", v)
	case "playlist-create":
		if strings.TrimSpace(value) == "" {
			return "", nil, fmt.Errorf("Playlist name required")
		}
		return "playlist/create", map[string]string{"name": value}, nil
	case "playlist-add-current":
		id, e := strconv.Atoi(value)
		if e != nil || id < 1 {
			return "", nil, fmt.Errorf("Playlist ID required")
		}
		return "script/add-current", map[string]int{"playlist": id}, nil
	case "dynamic":
		if value != "on" && value != "off" {
			return "", nil, fmt.Errorf("dynamic: on|off")
		}
		return "script/dynamic", map[string]bool{"enabled": value == "on"}, nil
	}
	return "", nil, fmt.Errorf("Unknown command: %s", cmd)
}
func (a *App) scriptAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, "script/") {
		return false
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return true
	}
	fail := func(e error) { w.WriteHeader(400); json.NewEncoder(w).Encode(map[string]string{"error": e.Error()}) }
	if path == "script/dynamic" {
		var b struct {
			Enabled bool `json:"enabled"`
		}
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			fail(e)
			return true
		}
		if e := a.mutateConfig(func(c *Config) { c.Dynamic.Enabled = b.Enabled }); e != nil {
			fail(e)
		} else {
			a.mu.Lock()
			a.commandSeq++
			v := 0.0
			if b.Enabled {
				v = 1
			}
			a.commands = append(a.commands, PlayerCommand{Seq: a.commandSeq, Action: "dynamic", Value: v})
			a.mu.Unlock()
			json.NewEncoder(w).Encode(true)
		}
		return true
	}
	if path == "script/add-current" {
		var b struct {
			Playlist int `json:"playlist"`
		}
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			fail(e)
			return true
		}
		a.mu.Lock()
		id := a.player.ID
		a.mu.Unlock()
		if !validTrackID.MatchString(id) {
			fail(fmt.Errorf("Немає поточного треку"))
			return true
		}
		if e := a.addToPlaylist(int64(b.Playlist), []string{id}); e != nil {
			fail(e)
		} else {
			json.NewEncoder(w).Encode(true)
		}
		return true
	}
	w.WriteHeader(404)
	return true
}
