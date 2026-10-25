package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"saymusic/internal/nativehost"
	"saymusic/internal/player"
	"strings"
)

const extensionID = "pfapcgdhfglkpbibnidhiagdbpndcein"

func main() {
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "chrome-extension://") {
		runBridge()
		return
	}
	player.Run()
}
func runBridge() {
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "chrome-extension://") && arg != "chrome-extension://"+extensionID+"/" {
			_ = nativehost.WriteMessage(os.Stdout, map[string]any{"ok": false, "error": "Розширення не дозволено"})
			return
		}
	}
	dir, e := os.UserConfigDir()
	if e != nil {
		_ = nativehost.WriteMessage(os.Stdout, map[string]any{"ok": false, "error": "Недоступна папка налаштувань"})
		return
	}
	exe, _ := os.Executable()
	name := "SayMusic"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	app := filepath.Join(filepath.Dir(exe), name)
	_ = nativehost.Run(os.Stdin, os.Stdout, filepath.Join(dir, "SayMusic"), func() error {
		cmd := exec.Command(app)
		if e := cmd.Start(); e != nil {
			return e
		}
		go cmd.Wait()
		return nil
	})
}
