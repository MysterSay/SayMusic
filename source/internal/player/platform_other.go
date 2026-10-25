//go:build !windows

package player

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func initProcessGroup() bool { return true }
func quiet(c *exec.Cmd)      {}
func focusMusicWindow()      {}
func openWindow(link, dir string, done <-chan struct{}, a *App) bool {
	if os.Getenv("SAY_MUSIC_HEADLESS") == "1" {
		return false
	}
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	c := exec.Command(name, link)
	if c.Start() == nil {
		go c.Wait()
	}
	return false
}
func showFatal(s string)                       { fmt.Fprintln(os.Stderr, "Say Music:", s) }
func replaceFile(src, dst string) error        { return os.Rename(src, dst) }
func openShared(path string) (*os.File, error) { return os.Open(path) }
func chooseFolder(language string) (string, error) {
	return "", errors.New("Введи шлях папки вручну")
}
func browserExecutable(name string) (string, error) {
	for _, n := range map[string][]string{"chrome": {"google-chrome", "chromium"}, "edge": {"microsoft-edge"}, "brave": {"brave-browser"}}[name] {
		if p, e := exec.LookPath(n); e == nil {
			return p, nil
		}
	}
	return "", errors.New("Браузер не знайдено")
}

func launchBrowser(path, link string) error {
	c := exec.Command(path, link)
	if e := c.Start(); e != nil {
		return e
	}
	go c.Wait()
	return nil
}
