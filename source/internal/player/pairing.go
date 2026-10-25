package player

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const pairAddress = "127.0.0.1:17843"
const pairRequestAAD = "say-music-request-v1"
const pairResponseAAD = "say-music-response-v1"

type pairEnvelope struct {
	Version int    `json:"version"`
	IV      string `json:"iv"`
	Data    string `json:"data"`
}
type pairMessage struct {
	ID      string          `json:"id"`
	Time    int64           `json:"time"`
	Message json.RawMessage `json:"message"`
}
type sessionPackage struct {
	Provider   string          `json:"provider"`
	Format     string          `json:"format"`
	Version    int             `json:"version"`
	CreatedAt  int64           `json:"createdAt"`
	PairingKey string          `json:"pairingKey"`
	Cookies    []browserCookie `json:"cookies"`
}

func pairCipher(key string) (cipher.AEAD, error) {
	b, e := hex.DecodeString(key)
	if e != nil || len(b) != 32 {
		return nil, errors.New("Некоректний ключ з’єднання")
	}
	block, e := aes.NewCipher(b)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func sealPair(key, aad string, data []byte) (pairEnvelope, error) {
	gcm, e := pairCipher(key)
	if e != nil {
		return pairEnvelope{}, e
	}
	iv := make([]byte, gcm.NonceSize())
	if _, e = rand.Read(iv); e != nil {
		return pairEnvelope{}, e
	}
	encrypted := gcm.Seal(nil, iv, data, []byte(aad))
	return pairEnvelope{1, base64.RawURLEncoding.EncodeToString(iv), base64.RawURLEncoding.EncodeToString(encrypted)}, nil
}
func openPair(key, aad string, envelope pairEnvelope) ([]byte, error) {
	if envelope.Version != 1 {
		return nil, errors.New("Непідтримувана версія з’єднання")
	}
	gcm, e := pairCipher(key)
	if e != nil {
		return nil, e
	}
	iv, e := base64.RawURLEncoding.DecodeString(envelope.IV)
	if e != nil || len(iv) != gcm.NonceSize() {
		return nil, errors.New("Некоректне повідомлення")
	}
	data, e := base64.RawURLEncoding.DecodeString(envelope.Data)
	if e != nil {
		return nil, errors.New("Некоректне повідомлення")
	}
	return gcm.Open(nil, iv, data, []byte(aad))
}
func (a *App) loadPair() {
	a.pairMu.Lock()
	defer a.pairMu.Unlock()
	b, e := os.ReadFile(filepath.Join(a.dir, "pair.dat"))
	if os.IsNotExist(e) {
		return
	}
	open := a.openPrivate
	if open == nil {
		open = unprotectSecret
	}
	if e == nil {
		b, e = open(b)
	}
	var record struct {
		Key string `json:"key"`
	}
	if e == nil {
		e = json.Unmarshal(b, &record)
	}
	if e == nil {
		_, e = pairCipher(record.Key)
	}
	if e != nil {
		a.pairError = "Не вдалося прочитати ключ зв’язку. Повторно скопіюй сесію з розширення та встав у програму"
		return
	}
	a.pairKey = record.Key
	a.pairReplay = map[string]time.Time{}
}
func (a *App) initPairBridge() { a.loadPair(); a.startPairBridge() }
func (a *App) startPairBridge() {
	a.pairMu.Lock()
	defer a.pairMu.Unlock()
	if a.pairServer != nil {
		return
	}
	listener, e := net.Listen("tcp", pairAddress)
	if e != nil {
		a.pairError = "Локальний порт 17843 зайнятий або недоступний. Закрий іншу копію Say Music та натисни «Повторити з’єднання»"
		return
	}
	server := &http.Server{Handler: http.HandlerFunc(a.pairHandler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	a.pairServer = server
	go func() { _ = server.Serve(listener) }()
}
func (a *App) closePairBridge() {
	a.pairMu.Lock()
	server := a.pairServer
	a.pairServer = nil
	a.pairMu.Unlock()
	if server != nil {
		server.Close()
	}
}
func (a *App) pairState() map[string]any {
	a.pairMu.Lock()
	defer a.pairMu.Unlock()
	return map[string]any{"paired": a.pairKey != "", "listening": a.pairServer != nil, "error": a.pairError}
}
func (a *App) importSession(text string) error {
	if a.config().OfflineOnly {
		return errors.New("Вимкни офлайн-режим перед підключенням сесії")
	}
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "SAYMUSIC_SESSION_V1"))
	var pkg sessionPackage
	if len(text) > 1<<20 || json.Unmarshal([]byte(text), &pkg) != nil || pkg.Format != "say-music-session" || pkg.Version != 1 {
		return errors.New("Скопіюй сесію кнопкою «Копіювати сесію» у розширенні Say Music та встав сюди весь текст")
	}
	if _, e := pairCipher(pkg.PairingKey); e != nil {
		return e
	}
	age := time.Now().UnixMilli() - pkg.CreatedAt
	if age < -180000 || age > 10*60*1000 {
		return errors.New("Копія сесії застаріла або час на ПК змінився. Скопіюй її з розширення ще раз")
	}
	jar, ok := cookieJar(pkg.Cookies)
	spotify := pkg.Provider == "spotify"
	if pkg.Provider != "" && pkg.Provider != "youtube" && !spotify {
		return errors.New("Невідомий сервіс сесії")
	}
	if spotify {
		if _, e := validateSpotifyCookies(pkg.Cookies); e != nil {
			return e
		}
	}
	if !ok && !spotify {
		return errors.New("У пакеті немає активної сесії YouTube. Увійди на music.youtube.com та скопіюй сесію повторно")
	}
	a.cancelAuth()
	// Serialize pairing changes and encrypt the persistent connection key with Windows DPAPI.
	a.pairMu.Lock()
	defer a.pairMu.Unlock()
	record, _ := json.Marshal(map[string]string{"key": pkg.PairingKey})
	seal := a.sealPrivate
	if seal == nil {
		seal = protectSecret
	}
	protected, e := seal(record)
	if e != nil {
		return e
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if !spotify {
		if e = atomicWrite(filepath.Join(a.dir, "cookies.txt"), jar); e != nil {
			return e
		}
	} else {
		if e = a.storeSpotifySession(pkg.Cookies); e != nil {
			return e
		}
	}
	if e = atomicWrite(filepath.Join(a.dir, "pair.dat"), protected); e != nil {
		return e
	}
	if e = a.mutateConfig(func(c *Config) {
		if spotify {
			c.SpotifyConnected = true
		} else {
			c.Cookies = true
		}
	}); e != nil {
		return e
	}
	a.pairKey = pkg.PairingKey
	a.pairReplay = map[string]time.Time{}
	a.pairError = ""
	a.mu.Lock()
	a.authStatus = AuthStatus{Status: "done", Message: "Сесію вставлено. Розширення підключене через локальний канал"}
	a.bridgeEvent++
	a.mu.Unlock()
	return nil
}
func (a *App) forgetPair() error {
	a.pairMu.Lock()
	defer a.pairMu.Unlock()
	e := os.Remove(filepath.Join(a.dir, "pair.dat"))
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	a.pairKey = ""
	a.pairReplay = nil
	a.pairError = ""
	return nil
}
func (a *App) pairAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, "pair/") {
		return false
	}
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	fail := func(e error) { w.WriteHeader(400); reply(map[string]string{"error": e.Error()}) }
	if path == "pair/status" {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
		} else {
			reply(a.pairState())
		}
		return true
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return true
	}
	switch path {
	case "pair/import":
		var b struct {
			Text string `json:"text"`
		}
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			fail(errors.New("Некоректний пакет сесії"))
		} else if e = a.importSession(b.Text); e != nil {
			fail(e)
		} else {
			a.startPairBridge()
			reply(a.pairState())
		}
	case "pair/forget":
		if e := a.forgetPair(); e != nil {
			fail(e)
		} else {
			reply(true)
		}
	case "pair/retry":
		a.pairMu.Lock()
		a.pairError = ""
		a.pairMu.Unlock()
		a.startPairBridge()
		reply(a.pairState())
	default:
		http.NotFound(w, r)
	}
	return true
}

type pairReplyWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *pairReplyWriter) Header() http.Header         { return w.header }
func (w *pairReplyWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *pairReplyWriter) WriteHeader(status int)      { w.status = status }
func (a *App) pairHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Host != pairAddress || r.URL.Path != "/pair/bridge" {
		http.Error(w, "Not found", 404)
		return
	}
	origin := r.Header.Get("Origin")
	allowedOrigin := "chrome-extension://" + extensionID
	if origin != "" && origin != allowedOrigin {
		http.Error(w, "Invalid origin", 403)
		return
	}
	if origin == allowedOrigin {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Vary", "Origin")
	}
	if r.Method == "OPTIONS" {
		if origin != allowedOrigin {
			http.Error(w, "Forbidden", 403)
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "content-type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.WriteHeader(204)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	a.pairMu.Lock()
	key := a.pairKey
	a.pairMu.Unlock()
	if key == "" {
		http.Error(w, "Pairing required", 401)
		return
	}
	var envelope pairEnvelope
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	if json.NewDecoder(r.Body).Decode(&envelope) != nil {
		http.Error(w, "Invalid message", 400)
		return
	}
	plaintext, e := openPair(key, pairRequestAAD, envelope)
	if e != nil {
		http.Error(w, "Pairing required", 401)
		return
	}
	var command pairMessage
	if json.Unmarshal(plaintext, &command) != nil || len(command.ID) != 32 || len(command.Message) == 0 {
		http.Error(w, "Invalid message", 400)
		return
	}
	age := time.Now().UnixMilli() - command.Time
	if age < -60000 || age > 60000 {
		http.Error(w, "Expired message", 400)
		return
	}
	a.pairMu.Lock()
	if key != a.pairKey {
		a.pairMu.Unlock()
		http.Error(w, "Pairing required", 401)
		return
	}
	now := time.Now()
	for id, expiry := range a.pairReplay {
		if now.After(expiry) {
			delete(a.pairReplay, id)
		}
	}
	if _, seen := a.pairReplay[command.ID]; seen {
		a.pairMu.Unlock()
		http.Error(w, "Repeated message", 409)
		return
	}
	if len(a.pairReplay) >= 1024 {
		a.pairMu.Unlock()
		http.Error(w, "Too many messages", 429)
		return
	}
	if a.pairReplay == nil {
		a.pairReplay = map[string]time.Time{}
	}
	a.pairReplay[command.ID] = now.Add(2 * time.Minute)
	a.pairMu.Unlock()
	inner := r.Clone(r.Context())
	inner.Body = io.NopCloser(bytes.NewReader(command.Message))
	inner.ContentLength = int64(len(command.Message))
	recorder := &pairReplyWriter{header: http.Header{}, status: 200}
	a.bridge(recorder, inner)
	response, _ := json.Marshal(struct {
		ID    string          `json:"id"`
		Reply json.RawMessage `json:"reply"`
	}{command.ID, recorder.body.Bytes()})
	result, e := sealPair(key, pairResponseAAD, response)
	if e != nil {
		http.Error(w, "Response failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
