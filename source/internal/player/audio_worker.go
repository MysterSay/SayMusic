package player

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Native Core Audio never runs in the process which owns the player window.
func (a *App) startDynamicAudio()           { go a.superviseAudio() }
func (a *App) setAudioLevels(s AudioLevels) { a.mu.Lock(); a.audioLevels = s; a.mu.Unlock() }
func (a *App) superviseAudio() {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	retryAt := time.Time{}
	for {
		select {
		case <-a.life.Done():
			return
		case <-tick.C:
			if !a.config().Dynamic.Enabled || time.Now().Before(retryAt) {
				continue
			}
			exe, e := os.Executable()
			if e != nil {
				a.setAudioLevels(AudioLevels{Error: e.Error()})
				continue
			}
			cmd := exec.CommandContext(a.life, exe, "--audio-worker", strconv.Itoa(os.Getpid()))
			quiet(cmd)
			if e = a.runAudioWorker(cmd, 4*time.Second); e != nil && a.life.Err() == nil && a.config().Dynamic.Enabled {
				log.Printf("Audio monitor restarted: %v", e)
				a.setAudioLevels(AudioLevels{Error: "Моніторинг звуку: " + e.Error()})
				retryAt = time.Now().Add(5 * time.Second)
			}
		}
	}
}
func (a *App) runAudioWorker(cmd *exec.Cmd, timeout time.Duration) (result error) {
	in, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := cmd.StdoutPipe()
	if e != nil {
		return e
	}
	defer out.Close()
	diagnostics := &audioWorkerDiagnostics{}
	cmd.Stderr = diagnostics
	if e = cmd.Start(); e != nil {
		return e
	}
	defer func() {
		cmd.Process.Kill()
		in.Close()
		out.Close()
		exitErr := cmd.Wait()
		if result != nil {
			if exitErr != nil {
				result = fmt.Errorf("%w; worker: %v", result, exitErr)
			}
			if detail := strings.TrimSpace(string(diagnostics.data)); detail != "" {
				result = fmt.Errorf("%w; %s", result, detail)
			}
		}
	}()
	stopped := make(chan struct{})
	defer close(stopped)
	// Pipe writes cannot hold the player state lock or UI thread.
	go func() {
		encoder := json.NewEncoder(in)
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			if encoder.Encode(a.config().Dynamic) != nil {
				return
			}
			select {
			case <-stopped:
				return
			case <-t.C:
			}
		}
	}()
	levels := make(chan AudioLevels, 1)
	finished := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var s AudioLevels
			if e := json.Unmarshal(scanner.Bytes(), &s); e != nil {
				continue
			}
			select {
			case levels <- s:
			case <-stopped:
				return
			}
		}
		e := scanner.Err()
		if e == nil {
			e = io.EOF
		}
		finished <- e
	}()
	watchdog := time.NewTimer(timeout)
	defer watchdog.Stop()
	check := time.NewTicker(200 * time.Millisecond)
	defer check.Stop()
	for {
		select {
		case <-a.life.Done():
			return a.life.Err()
		case <-check.C:
			if !a.config().Dynamic.Enabled {
				a.setAudioLevels(AudioLevels{})
				return nil
			}
		case e := <-finished:
			return e
		case <-watchdog.C:
			return fmt.Errorf("Core Audio did not respond within %s", timeout)
		case s := <-levels:
			a.setAudioLevels(s)
			if !watchdog.Stop() {
				select {
				case <-watchdog.C:
				default:
				}
			}
			watchdog.Reset(timeout)
		}
	}
}

// RunAudioWorker uses only stdin/stdout JSON and has no UI or on-disk state.
func RunAudioWorker(parent string) int {
	pid, e := strconv.ParseUint(parent, 10, 32)
	if e != nil || pid == 0 {
		return 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{life: ctx, audioParentPID: uint32(pid)}
	go func() {
		decoder := json.NewDecoder(os.Stdin)
		for {
			var c DynamicAudioConfig
			if decoder.Decode(&c) != nil {
				cancel()
				return
			}
			a.mu.Lock()
			a.cfg.Dynamic = normalizeDynamic(c)
			a.mu.Unlock()
		}
	}()
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		encoder := json.NewEncoder(os.Stdout)
		var last uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.mu.Lock()
				s := a.audioLevels
				seq := a.audioSampleSeq
				a.mu.Unlock()
				if seq == last {
					continue
				}
				last = seq
				if encoder.Encode(s) != nil {
					cancel()
					return
				}
			}
		}
	}()
	runAudioMonitor(a, monitorAudio)
	return 0
}

// WebView2 initializes and locks the entry thread as STA during package init.
// Core Audio needs an MTA thread. Always enter the native monitor from a new
// goroutine; monitorAudio locks that thread for its complete COM lifetime.
func runAudioMonitor(a *App, monitor func(*App)) {
	done := make(chan struct{})
	go func() { defer close(done); monitor(a) }()
	<-done
}

// Bound crash diagnostics; cmd.Wait joins the stderr copy before reading data.
type audioWorkerDiagnostics struct{ data []byte }

func (b *audioWorkerDiagnostics) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 2048 - len(b.data); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
