package player

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"strings"
	"time"
)

type IslandConfig struct {
	EdgeLength         int    `json:"edgeLength"`
	EdgeThickness      int    `json:"edgeThickness"`
	EdgeReaction       bool   `json:"edgeReaction"`
	EdgeReactionRadius int    `json:"edgeReactionRadius"`
	EdgeFree           bool   `json:"edgeFree"`
	EdgeSide           string `json:"edgeSide"`
	EdgeOffset         int    `json:"edgeOffset"`
	EdgeMonitorX       int    `json:"edgeMonitorX"`
	EdgeMonitorY       int    `json:"edgeMonitorY"`
	EdgeStandard       bool   `json:"edgeStandard"`
	EdgeColor          string `json:"edgeColor"`
	Enabled            bool   `json:"enabled"`
	CloseToTray        bool   `json:"closeToTray"`
	Layout             string `json:"layout"`
	Theme              string `json:"theme"`
	Material           string `json:"material"`
	Position           string `json:"position"`
	Width              int    `json:"width"`
	Cover              int    `json:"cover"`
	Radius             int    `json:"radius"`
	Opacity            int    `json:"opacity"`
	OffsetX            int    `json:"offsetX"`
	OffsetY            int    `json:"offsetY"`
	Topmost            bool   `json:"topmost"`
	Hover              bool   `json:"hover"`
	Animations         bool   `json:"animations"`
	NativeBackdrop     bool   `json:"nativeBackdrop"`
	BackgroundColor    string `json:"backgroundColor"`
	BackgroundImage    string `json:"backgroundImage"`
}

// Missing fields in older settings retain the default linked stripe color.
func (c *IslandConfig) UnmarshalJSON(data []byte) error {
	type plain IslandConfig
	v := plain{EdgeStandard: true, EdgeReaction: true, EdgeReactionRadius: 150, EdgeLength: 90, EdgeThickness: 10}
	if e := json.Unmarshal(data, &v); e != nil {
		return e
	}
	*c = IslandConfig(v)
	return nil
}

func oneOf(s string, choices ...string) bool {
	for _, c := range choices {
		if s == c {
			return true
		}
	}
	return false
}
func normalizeIsland(c IslandConfig) IslandConfig {
	if c.EdgeLength == 0 {
		c.EdgeLength = 90
	}
	if c.EdgeThickness == 0 {
		c.EdgeThickness = 10
	}
	if c.EdgeReactionRadius == 0 {
		c.EdgeReactionRadius = 150
		c.EdgeReaction = true
	}
	c.EdgeLength = clamp(c.EdgeLength, 30, 400)
	c.EdgeThickness = clamp(c.EdgeThickness, 2, 32)
	c.EdgeReactionRadius = clamp(c.EdgeReactionRadius, 20, 600)
	if c.EdgeColor == "" {
		c.EdgeStandard = true
		c.EdgeColor = "#ae96ff"
	}
	if !oneOf(c.EdgeSide, "left", "right", "top", "bottom") {
		c.EdgeSide = "right"
		c.EdgeOffset = 5000
	}
	c.EdgeOffset = clamp(c.EdgeOffset, 0, 10000)
	c.EdgeColor = interfaceThemeColor(c.EdgeColor)
	c.NativeBackdrop = false // Ignore old experimental settings on upgrade and save.
	if c.Width == 0 {
		edge := c
		c = IslandConfig{Enabled: c.Enabled, CloseToTray: c.CloseToTray, Layout: "full", Theme: "dark", Material: "acrylic", Position: "bottom-right", Width: 280, Cover: 240, Radius: 24, Opacity: 94, OffsetX: 20, OffsetY: 20, Topmost: true, Hover: true, Animations: true}
		c.EdgeLength, c.EdgeThickness = edge.EdgeLength, edge.EdgeThickness
		c.EdgeReaction, c.EdgeReactionRadius = edge.EdgeReaction, edge.EdgeReactionRadius
		c.EdgeFree = edge.EdgeFree
		c.EdgeSide = edge.EdgeSide
		c.EdgeOffset = edge.EdgeOffset
		c.EdgeMonitorX = edge.EdgeMonitorX
		c.EdgeMonitorY = edge.EdgeMonitorY
		c.EdgeColor = edge.EdgeColor
		c.EdgeStandard = edge.EdgeStandard
	}
	if len(c.BackgroundColor) != 7 || c.BackgroundColor[0] != '#' {
		c.BackgroundColor = "#18191d"
	}
	if _, err := hex.DecodeString(c.BackgroundColor[1:]); err != nil {
		c.BackgroundColor = "#18191d"
	}
	if !validIslandBackground(c.BackgroundImage) {
		c.BackgroundImage = ""
	}
	c.Width = clamp(c.Width, 240, 560)
	c.Cover = clamp(c.Cover, 64, 420)
	c.Radius = clamp(c.Radius, 0, 48)
	c.Opacity = clamp(c.Opacity, 40, 100)
	c.OffsetX = clamp(c.OffsetX, 0, 2000)
	c.OffsetY = clamp(c.OffsetY, 0, 2000)
	if !oneOf(c.Layout, "full", "compact", "side") {
		c.Layout = "full"
	}
	if !oneOf(c.Theme, "dark", "light") {
		c.Theme = "dark"
	}
	if !oneOf(c.Material, "solid", "mica", "acrylic", "glass", "transparent", "borderless", "custom") {
		c.Material = "acrylic"
	}
	if !oneOf(c.Position, "bottom-right", "bottom-left", "top-right", "top-left", "bottom-center") {
		c.Position = "bottom-right"
	}
	return c
}
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type PlayerState struct {
	Duration float64 `json:"duration"`
	ID       string  `json:"id"`
	Track    Track   `json:"track"`
	Playing  bool    `json:"playing"`
	Position float64 `json:"position"`
	Volume   float64 `json:"volume"`
	After    uint64  `json:"after"`
}
type PlayerCommand struct {
	Seq    uint64  `json:"seq"`
	Action string  `json:"action"`
	Value  float64 `json:"value"`
}

func (a *App) experienceAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, "player/") && !strings.HasPrefix(path, "desktop/") && !oneOf(path, "artists", "artist/tracks", "recommendations", "experience/settings", "appearance/settings", "island/background") {
		return false
	}
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	fail := func(s string) { w.WriteHeader(400); reply(map[string]string{"error": s}) }
	get := oneOf(path, "island/background", "player/status", "artists", "artist/tracks")
	if (get && r.Method != "GET") || (!get && r.Method != "POST") {
		http.Error(w, "Method not allowed", 405)
		return true
	}
	switch path {
	case "player/error":
		var b struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || !validTrackID.MatchString(b.ID) {
			fail("Некоректний трек")
			break
		}
		t, e := a.track(b.ID)
		if e != nil {
			fail("Трек не знайдено")
			break
		}
		source := "youtube"
		if strings.HasPrefix(t.ID, "sp_") || strings.HasPrefix(t.ID, "sm_") {
			source = "spotify"
		}
		if e = writeIssue(a.db, TrackIssue{ID: "track:" + t.ID, TrackID: t.ID, Source: source, Title: t.Title, Artist: t.Artist, URL: t.URL, Kind: "playback_error", Reason: b.Reason}); e != nil {
			fail(e.Error())
		} else {
			reply(true)
		}
	case "island/background":
		value := a.config().Island.BackgroundImage
		if value == "" {
			http.NotFound(w, r)
			break
		}
		parts := strings.SplitN(value, ",", 2)
		if len(parts) != 2 {
			http.Error(w, "Invalid image", 500)
			break
		}
		raw, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			http.Error(w, "Invalid image", 500)
			break
		}
		w.Header().Set("Content-Type", strings.TrimSuffix(strings.TrimPrefix(parts[0], "data:"), ";base64"))
		w.Write(raw)

	case "artists":
		rows, e := a.db.Query(`SELECT artist,count(*),min(thumbnail) FROM active_tracks GROUP BY artist ORDER BY artist COLLATE NOCASE`)
		if e != nil {
			fail(e.Error())
			break
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var name, img string
			var count int
			rows.Scan(&name, &count, &img)
			out = append(out, map[string]any{"name": name, "count": count, "thumbnail": img})
		}
		reply(out)
	case "artist/tracks":
		rows, e := a.db.Query(`SELECT id,title,artist,url,thumbnail,duration FROM active_tracks WHERE artist=? ORDER BY title COLLATE NOCASE`, r.URL.Query().Get("name"))
		if e != nil {
			fail(e.Error())
			break
		}
		ts, e := scanTracks(rows)
		if e != nil {
			fail(e.Error())
		} else {
			reply(a.markCached(ts))
		}
	case "appearance/settings":
		var b struct {
			InterfaceColor  string  `json:"interfaceColor"`
			InterfaceAccent *string `json:"interfaceAccent"`
			InterfaceTheme  string  `json:"interfaceTheme"`
			Language        *string `json:"language"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			fail("Некоректні налаштування")
			break
		}
		if b.Language != nil && !oneOf(*b.Language, "uk", "en") {
			fail("Некоректна мова")
			break
		}
		if b.InterfaceAccent != nil && *b.InterfaceAccent != interfaceThemeColor(*b.InterfaceAccent) {
			fail("Некоректний додатковий колір")
			break
		}
		if !oneOf(b.InterfaceTheme, "dark", "light", "system") || b.InterfaceColor != interfaceThemeColor(b.InterfaceColor) {
			fail("Обери тему та колір інтерфейсу")
			break
		}
		if e := a.mutateConfig(func(c *Config) {
			if b.Language != nil {
				c.Language = *b.Language
			}
			c.InterfaceColor = b.InterfaceColor
			c.InterfaceTheme = b.InterfaceTheme
			if b.InterfaceAccent != nil {
				c.InterfaceAccent = *b.InterfaceAccent
			}
		}); e != nil {
			fail(e.Error())
			break
		}
		a.mu.Lock()
		desktop := a.desktop
		a.mu.Unlock()
		if desktop != nil {
			desktop("apply")
		}
		reply(a.config())
	case "experience/settings":
		var b struct {
			Island          IslandConfig `json:"island"`
			Recommendations bool         `json:"recommendations"`
			EarlyBuffering  *bool        `json:"earlyBuffering"`
			PrefetchSeconds *int         `json:"prefetchSeconds"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			fail("Некоректні налаштування")
			break
		}
		b.Island = normalizeIsland(b.Island)
		if e := a.mutateConfig(func(c *Config) {
			// The native drag owns the anchor. A settings dialog opened before
			// dragging must not overwrite the new position with stale fields.
			anchor := c.Island
			next := b.Island
			next.EdgeSide = anchor.EdgeSide
			next.EdgeOffset = anchor.EdgeOffset
			next.EdgeMonitorX = anchor.EdgeMonitorX
			next.EdgeMonitorY = anchor.EdgeMonitorY
			c.Island = normalizeIsland(next)
			c.Recommendations = b.Recommendations
			if b.EarlyBuffering != nil {
				c.EarlyBuffering = b.EarlyBuffering
			}
			if b.PrefetchSeconds != nil {
				c.PrefetchSeconds = clamp(*b.PrefetchSeconds, 10, 180)
			}
		}); e != nil {
			fail(e.Error())
			break
		}
		a.mu.Lock()
		desktop := a.desktop
		a.mu.Unlock()
		if desktop != nil {
			desktop("apply")
		}
		reply(a.config())
	case "desktop/action":
		var b struct {
			Action string `json:"action"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if !oneOf(b.Action, "tray", "restore", "island", "hide-island", "expand", "collapse", "pin", "unpin", "collapse-force") {
			fail("Невідома дія")
			break
		}
		a.mu.Lock()
		f := a.desktop
		a.mu.Unlock()
		if f == nil {
			fail("Керування вікнами доступне у Windows")
		} else if e := f(b.Action); e != nil {
			fail(e.Error())
		} else {
			reply(true)
		}
	case "player/status":
		a.mu.Lock()
		p := a.player
		a.mu.Unlock()
		c := a.config()
		island := c.Island
		if island.BackgroundImage != "" {
			digest := sha256.Sum256([]byte(island.BackgroundImage))
			island.BackgroundImage = fmt.Sprintf("%x", digest[:8])
		}
		var favorite bool
		_ = a.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM favorites WHERE track_id=?)`, p.Track.ID).Scan(&favorite)
		reply(map[string]any{"favorite": favorite, "player": p, "island": island, "offline": c.OfflineOnly, "interfaceColor": interfaceThemeColor(c.InterfaceColor), "language": c.Language, "interfaceTheme": c.InterfaceTheme})
	case "player/tick":
		var p PlayerState
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			fail("Некоректний стан")
			break
		}
		if math.IsNaN(p.Position) || p.Position < 0 {
			p.Position = 0
		}
		if p.Volume < 0 || p.Volume > 1 {
			p.Volume = .7
		}
		if validTrackID.MatchString(p.ID) {
			t, e := a.track(p.ID)
			if e == nil {
				if t.Duration == 0 && p.Duration > 0 && p.Duration < 86400 {
					t.Duration = p.Duration
					_, _ = a.db.Exec(`UPDATE tracks SET duration=? WHERE id=? AND duration=0`, p.Duration, p.ID)
				}
				p.Track = t
			}
		}
		a.mu.Lock()
		a.player = p
		out := []PlayerCommand{}
		for _, c := range a.commands {
			if c.Seq > p.After {
				out = append(out, c)
			}
		}
		a.mu.Unlock()
		reply(out)
	case "player/command":
		var c PlayerCommand
		if json.NewDecoder(r.Body).Decode(&c) != nil || !oneOf(c.Action, "toggle", "next", "previous", "seek", "volume") {
			fail("Некоректна команда")
			break
		}
		if c.Value < 0 || math.IsNaN(c.Value) {
			c.Value = 0
		}
		a.mu.Lock()
		a.commandSeq++
		c.Seq = a.commandSeq
		a.commands = append(a.commands, c)
		if len(a.commands) > 64 {
			a.commands = a.commands[len(a.commands)-64:]
		}
		a.mu.Unlock()
		reply(true)
	case "player/played":
		var b struct {
			ID string `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if !validTrackID.MatchString(b.ID) {
			fail("Некоректний трек")
			break
		}
		_, e := a.db.Exec(`INSERT INTO listening_history VALUES(?,1,?) ON CONFLICT(track_id) DO UPDATE SET plays=plays+1,last_played=excluded.last_played WHERE last_played<excluded.last_played-60`, b.ID, time.Now().Unix())
		if e != nil {
			fail(e.Error())
		} else {
			reply(true)
		}
	case "recommendations":
		if !a.config().Recommendations {
			fail("Увімкни рекомендації в налаштуваннях")
			break
		}
		a.recommend(w, r)
	}
	return true
}
func (a *App) recommend(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Refresh bool   `json:"refresh"`
		Seed    string `json:"seed"`
	}
	json.NewDecoder(r.Body).Decode(&b)
	rows, e := a.db.Query(`SELECT t.id,t.title,t.artist,t.url,t.thumbnail,t.duration FROM active_tracks t LEFT JOIN listening_history h ON h.track_id=t.id ORDER BY COALESCE(h.plays,0) DESC,COALESCE(h.last_played,0) DESC,t.added DESC LIMIT 20`)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	local, e := scanTracks(rows)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	result := []Track{}
	seen := map[string]bool{}
	add := func(t Track) {
		if !seen[t.ID] {
			seen[t.ID] = true
			result = append(result, t)
		}
	}
	note := "Улюблені з твоєї бібліотеки"
	var discovery []Track
	if len(local) > 0 && !a.config().OfflineOnly {
		seed := b.Seed
		if !validTrackID.MatchString(seed) {
			seed = local[0].ID
		}
		query := "say-recommendations:" + seed
		var raw string
		var created int64
		a.db.QueryRow(`SELECT ids,created FROM searches WHERE query=?`, query).Scan(&raw, &created)
		if !b.Refresh && time.Now().Unix()-created < 21600 && raw != "" {
			var ids []string
			json.Unmarshal([]byte(raw), &ids)
			for _, id := range ids {
				if t, e := a.track(id); e == nil {
					discovery = append(discovery, t)
				}
			}
		} else {
			select {
			case a.imports <- struct{}{}:
				defer func() { <-a.imports }()
				ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
				defer cancel()
				youtubeSeed, seedError := a.youtubeSeed(ctx, seed)
				if seedError != nil {
					e = seedError
				} else {
					discovery, e = a.extract(ctx, "https://www.youtube.com/watch?v="+youtubeSeed+"&list=RD"+youtubeSeed, 35, a.config().Cookies)
				}
				if e == nil {
					ids := []string{}
					for _, t := range discovery {
						if a.save(t) == nil {
							ids = append(ids, t.ID)
						}
					}
					encoded, _ := json.Marshal(ids)
					a.db.Exec(`INSERT OR REPLACE INTO searches VALUES(?,?,?)`, query, string(encoded), time.Now().Unix())
				} else {
					note = "Улюблені з бібліотеки. Схожі треки зараз недоступні: " + e.Error()
				}
			default:
				note = "Улюблені з бібліотеки. Інший імпорт ще виконується"
			}
		}
		if e == nil && len(discovery) > 0 {
			note = "Улюблені та схожі треки · добірка зберігається на 6 годин"
		}
	}
	for i := 0; i < len(local) || i < len(discovery); i++ {
		if i < len(local) {
			add(local[i])
		}
		if i < len(discovery) {
			add(discovery[i])
		}
	}
	if a.config().OfflineOnly {
		filtered := []Track{}
		for _, t := range a.markCached(result) {
			if t.Cached {
				filtered = append(filtered, t)
			}
		}
		result = filtered
		note = "Офлайн: лише збережені улюблені"
	}
	json.NewEncoder(w).Encode(map[string]any{"tracks": a.markCached(result), "message": note})
}

func validIslandBackground(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 1500000 {
		return false
	}
	var data string
	for _, prefix := range []string{"data:image/png;base64,", "data:image/jpeg;base64,"} {
		if strings.HasPrefix(value, prefix) {
			data = strings.TrimPrefix(value, prefix)
			break
		}
	}
	if data == "" {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	return err == nil && cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= 2048 && cfg.Height <= 2048
}

func interfaceThemeColor(value string) string {
	if len(value) == 7 && value[0] == '#' {
		if _, err := hex.DecodeString(value[1:]); err == nil {
			return value
		}
	}
	return "#191b23"
}
