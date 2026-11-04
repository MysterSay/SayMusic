package player

import (
	"encoding/json"
	"math"
	"net/http"
	"time"
)

type DynamicAudioConfig struct {
	Enabled    bool    `json:"enabled"`
	Delay      float64 `json:"delay"`
	Minimum    float64 `json:"minimum"`
	Maximum    float64 `json:"maximum"`
	Output     string  `json:"output"`
	Microphone bool    `json:"microphone"`
	Input      string  `json:"input"`
}
type AudioDevice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Capture bool   `json:"capture"`
}
type AudioLevels struct {
	Output  float64       `json:"output"`
	Input   float64       `json:"input"`
	Error   string        `json:"error,omitempty"`
	Devices []AudioDevice `json:"devices"`
}

func normalizeDynamic(c DynamicAudioConfig) DynamicAudioConfig {
	if c.Maximum == 0 {
		c.Maximum = 100
	}
	if c.Delay < 0 || math.IsNaN(c.Delay) {
		c.Delay = 2
	}
	if c.Delay > 120 {
		c.Delay = 120
	}
	c.Minimum = math.Max(0, math.Min(100, c.Minimum))
	c.Maximum = math.Max(c.Minimum, math.Min(100, c.Maximum))
	return c
}
func (a *App) startDynamicAudio() { go monitorAudio(a) }
func (a *App) dynamicAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "audio/dynamic" && path != "audio/levels" {
		return false
	}
	if path == "audio/levels" {
		a.mu.Lock()
		s := a.audioLevels
		a.mu.Unlock()
		json.NewEncoder(w).Encode(s)
		return true
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return true
	}
	var c DynamicAudioConfig
	if json.NewDecoder(r.Body).Decode(&c) != nil {
		http.Error(w, "Invalid settings", 400)
		return true
	}
	c = normalizeDynamic(c)
	if e := a.mutateConfig(func(x *Config) { x.Dynamic = c }); e != nil {
		http.Error(w, e.Error(), 400)
		return true
	}
	json.NewEncoder(w).Encode(c)
	return true
}

// Meter sampling is intentionally disabled when the feature is off.
func audioTick(a *App, sample func(DynamicAudioConfig) AudioLevels) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.life.Done():
			return
		case <-ticker.C:
			c := a.config().Dynamic
			if !c.Enabled {
				if a.audioWasEnabled {
					sample(c)
					a.audioWasEnabled = false
				}
				continue
			}
			a.audioWasEnabled = true
			levels := sample(c)
			a.mu.Lock()
			a.audioLevels = levels
			a.mu.Unlock()
		}
	}
}
