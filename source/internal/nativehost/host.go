package nativehost

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const MaxMessage = 8 << 20

func ReadMessage(r io.Reader) (json.RawMessage, error) {
	var size uint32
	if e := binary.Read(r, binary.LittleEndian, &size); e != nil {
		return nil, e
	}
	if size == 0 || size > MaxMessage {
		return nil, errors.New("Некоректний розмір повідомлення")
	}
	b := make([]byte, int(size))
	if _, e := io.ReadFull(r, b); e != nil {
		return nil, e
	}
	if !json.Valid(b) {
		return nil, errors.New("Некоректне JSON-повідомлення")
	}
	return b, nil
}
func WriteMessage(w io.Writer, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > MaxMessage {
		return errors.New("Завелика відповідь")
	}
	if e = binary.Write(w, binary.LittleEndian, uint32(len(b))); e != nil {
		return e
	}
	_, e = w.Write(b)
	return e
}

type Descriptor struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

var tokenPattern = regexp.MustCompile(`^[0-9a-f]{48}$`)

func readDescriptor(dir string) (Descriptor, error) {
	var d Descriptor
	b, e := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if e != nil {
		return d, e
	}
	if len(b) > 16384 {
		return d, errors.New("Некоректний файл підключення")
	}
	if e = json.Unmarshal(b, &d); e != nil {
		return d, e
	}
	u, e := url.Parse(d.URL)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.User != nil || u.Fragment != "" || !tokenPattern.MatchString(d.Token) {
		return d, errors.New("Некоректна локальна адреса Say Music")
	}
	return d, nil
}
func Forward(ctx context.Context, dir string, message json.RawMessage) (json.RawMessage, error) {
	d, e := readDescriptor(dir)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", d.URL+"/bridge?token="+url.QueryEscape(d.Token), bytes.NewReader(message))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("Перенаправлення не дозволено")
	}}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, MaxMessage))
	if e != nil {
		return nil, e
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("Say Music повернув HTTP %d", resp.StatusCode)
	}
	return b, nil
}

// One browser request, one short-lived host process. No cookies or credentials
// are written by the host, and stdout is reserved for native messaging frames.
func Run(in io.Reader, out io.Writer, dir string, start func() error) error {
	b, e := ReadMessage(in)
	if e != nil {
		return WriteMessage(out, map[string]any{"ok": false, "error": e.Error()})
	}
	var m struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(b, &m)
	switch m.Action {
	case "library/check", "add", "auth", "status", "open", "spotify/fetch", "spotify/status", "spotify/claim", "spotify/auth", "spotify/import", "spotify/error":
	default:
		return WriteMessage(out, map[string]any{"ok": false, "error": "Непідтримувана команда"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	response, e := Forward(ctx, dir, b)
	if e == nil && m.Action == "open" && start != nil {
		_ = start()
	}
	if e != nil && (m.Action == "status" || m.Action == "library/check") {
		return WriteMessage(out, map[string]any{"ok": true, "running": false, "authenticated": false})
	}
	if e != nil && start != nil {
		if e = start(); e == nil {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				response, e = Forward(ctx, dir, b)
				if e == nil {
					break
				}
				select {
				case <-ctx.Done():
					return WriteMessage(out, map[string]any{"ok": false, "error": "Не вдалося підключитися. Закрий стару версію Say Music й запусти нову через Start.cmd"})
				case <-ticker.C:
				}
			}
		}
	}
	if e != nil {
		return WriteMessage(out, map[string]any{"ok": false, "error": "Say Music недоступний. Запусти Start.cmd"})
	}
	return WriteMessage(out, response)
}
