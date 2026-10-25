//go:build windows

package player

import (
	"fmt"
	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var u32 = windows.NewLazySystemDLL("user32.dll")
var s32 = windows.NewLazySystemDLL("shell32.dll")
var d32 = windows.NewLazySystemDLL("dwmapi.dll")

type notifyIconData struct {
	Size                uint32
	Hwnd                uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Timeout             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                [16]byte
	BalloonIcon         uintptr
}

func signed(v int32) uintptr { return uintptr(int64(v)) }

func setIslandWindowStyle(handle uintptr) {
	old, _, _ := u32.NewProc("GetWindowLongPtrW").Call(handle, signed(-16))
	u32.NewProc("SetWindowLongPtrW").Call(handle, signed(-16), islandWindowStyle(old))
}
func installDesktop(w webview2.WebView, link, dir string, a *App) func() {
	hwnd := uintptr(w.Window())
	var backdrop uintptr
	var capsuleBackdrop uintptr
	mini := webview2.NewWithOptions(webview2.WebViewOptions{DataPath: filepath.Join(dir, "WebView"), AutoFocus: false, WindowOptions: webview2.WindowOptions{Title: "Say Music Island", Width: 280, Height: 400, IconId: 2}})
	var mh uintptr
	if mini != nil {
		mh = uintptr(mini.Window())
		u32.NewProc("ShowWindow").Call(mh, 0)
		if capsuleBackdrop != 0 {
			u32.NewProc("ShowWindow").Call(capsuleBackdrop, 0)
		}
		if backdrop != 0 {
			u32.NewProc("ShowWindow").Call(backdrop, 0)
		}
		_ = webview2.SetSurfaceBackground(mini, true, a.config().Island.Theme == "light")
		webview2.OnNavigation(mini, func(ok bool, status int32) {
			if !ok {
				log.Printf("Island panel navigation failed: status=%d", status)
				mini.SetHtml(`<body style="margin:0;padding:20px;background:#191a1d;color:white;font:14px Segoe UI">` + a.uiText("Не вдалося завантажити острівець. Закрий і знову відкрий Say Music.") + `</body>`)
				return
			}
			_ = webview2.RefreshSurface(mini)
		})
		mini.Navigate(strings.Replace(link, "/?", "/island.html?", 1))
	}
	capsule := webview2.NewWithOptions(webview2.WebViewOptions{DataPath: filepath.Join(dir, "WebView"), AutoFocus: false, WindowOptions: webview2.WindowOptions{Title: "Say Music Capsule", Width: 220, Height: 40, IconId: 2}})
	var ch uintptr
	if capsule != nil {
		ch = uintptr(capsule.Window())
		u32.NewProc("ShowWindow").Call(ch, 0)
		_ = webview2.SetSurfaceBackground(capsule, true, a.config().Island.Theme == "light")
		webview2.OnNavigation(capsule, func(ok bool, status int32) {
			if !ok {
				log.Printf("Island capsule navigation failed: status=%d", status)
				capsule.SetHtml(`<body style="margin:0;padding:8px;background:#191a1d;color:white;font:12px Segoe UI">` + a.uiText("Помилка острівця. Перезапусти Say Music.") + `</body>`)
				return
			}
			_ = webview2.RefreshSurface(capsule)
		})
		capsule.Navigate(strings.Replace(link, "/?", "/island.html?capsule=1&", 1))
	}
	var capsuleRect winRect
	var lastCapsuleRect winRect
	islandVisible := false
	trayRaised := false
	pinned := false

	inst, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	icon, _, _ := u32.NewProc("LoadIconW").Call(inst, 2)
	tray := notifyIconData{Hwnd: hwnd, ID: 1, Flags: 7, Callback: 0x8001, Icon: icon}
	tray.Size = uint32(unsafe.Sizeof(tray))
	copy(tray.Tip[:], syscall.StringToUTF16("Say Music"))
	trayAdded := false
	hidden := false
	expanded := false
	backdrop = createIslandBackdrop()
	// The taskbar capsule uses page tint over the taskbar itself.
	// A standalone accent window paints rectangular corner colorization.
	capsuleBackdrop = 0

	place := func(r winRect) {
		c := a.config().Island
		top := signed(-2)
		if c.Topmost {
			top = signed(-1)
		}
		ww, hh := r.Right-r.Left, r.Bottom-r.Top
		u32.NewProc("SetWindowPos").Call(mh, top, signed(r.Left), signed(r.Top), signed(ww), signed(hh), 0x0030)
		if visible, _, _ := u32.NewProc("IsWindowVisible").Call(mh); visible != 0 {
			_ = webview2.RefreshSurface(mini)
		}
		radius := int32(c.Radius)
		if !expanded {
			radius = 22
		}
		dpi, _, _ := u32.NewProc("GetDpiForWindow").Call(mh)
		if dpi == 0 {
			dpi = 96
		}
		radius = radius * int32(dpi) / 96
		rgn, _, _ := windows.NewLazySystemDLL("gdi32.dll").NewProc("CreateRoundRectRgn").Call(0, 0, signed(ww+1), signed(hh+1), signed(radius*2), signed(radius*2))
		if ok, _, _ := u32.NewProc("SetWindowRgn").Call(mh, rgn, 1); ok == 0 && rgn != 0 {
			windows.NewLazySystemDLL("gdi32.dll").NewProc("DeleteObject").Call(rgn)
		}
	}
	addTray := func() {
		if !trayAdded {
			ok, _, _ := s32.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&tray)))
			trayAdded = ok != 0
		}
	}
	removeTray := func() {
		if trayAdded {
			s32.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&tray)))
			trayAdded = false
		}
	}

	type backdropState struct {
		Rect            winRect
		Material, Theme string
		Radius          int
		Visible         bool
	}
	backdropStates := map[uintptr]backdropState{}
	updateBackdrop := func(handle, panel uintptr, rect winRect, c IslandConfig, visible bool) {
		if handle == 0 {
			return
		}
		visible = visible && (c.Material == "glass" || c.Material == "acrylic")
		next := backdropState{rect, c.Material, c.Theme, c.Radius, visible}
		actual, _, _ := u32.NewProc("IsWindowVisible").Call(handle)
		if previous, ok := backdropStates[handle]; ok && previous == next && (actual != 0) == visible {
			return
		}
		setIslandBackdrop(handle, panel, rect, c, visible)
		backdropStates[handle] = next
	}
	edgeBar := newEdgeOverlay(mh)
	apply := func() {
		applyMainWindowTheme(hwnd, a.config())
		if mini == nil {
			return
		}
		c := a.config().Island

		panelConfig := c
		if c.EdgeFree && panelConfig.Layout == "side" {
			panelConfig.Layout = "compact"
		}
		width, height := islandPanelSize(panelConfig, expanded || c.EdgeFree)
		dpi, _, _ := u32.NewProc("GetDpiForWindow").Call(mh)
		if dpi == 0 {
			dpi = 96
		}
		scale := func(v int) int32 { return int32(v * int(dpi) / 96) }
		var area winRect
		u32.NewProc("SystemParametersInfoW").Call(0x30, 0, uintptr(unsafe.Pointer(&area)), 0)
		if area.Right == 0 {
			area.Right = 1920
			area.Bottom = 1040
		}
		ww, hh := scale(width), scale(height)
		x, y := area.Right-ww-scale(c.OffsetX), area.Bottom-hh-scale(c.OffsetY)
		if strings.Contains(c.Position, "left") {
			x = area.Left + scale(c.OffsetX)
		}
		if strings.HasPrefix(c.Position, "top") {
			y = area.Top + scale(c.OffsetY)
		}
		if c.Position == "bottom-center" {
			x = area.Left + (area.Right-area.Left-ww)/2
		}
		if x < area.Left {
			x = area.Left
		}
		if y < area.Top {
			y = area.Top
		}
		setIslandWindowStyle(mh)
		u32.NewProc("SetWindowLongPtrW").Call(mh, signed(-20), 0x00000080)
		top := signed(-2)
		if c.Topmost {
			top = signed(-1)
		}
		if c.EdgeFree {
			area = monitorArea(int32(c.EdgeMonitorX), int32(c.EdgeMonitorY))
			area = edgeInsetArea(area)
			strip := edgeWidgetAnchor(area, c.EdgeSide, int32(c.EdgeOffset), int32(c.EdgeThickness), int32(c.EdgeLength))
			appConfig := a.config()
			light := appConfig.InterfaceTheme == "light" || (appConfig.InterfaceTheme == "system" && !windowsAppsUseDarkTheme())
			edgeBar.controlOpen = expanded
			if expanded {
				edgeBar.progress, edgeBar.target = 0, 0
			}
			edgeBar.side, edgeBar.thickness = c.EdgeSide, int32(c.EdgeThickness)
			edgeBar.reaction, edgeBar.reactionRadius = c.EdgeReaction, int32(c.EdgeReactionRadius)
			edgeBar.place(strip, edgeStripColor(c, appConfig.InterfaceAccent), islandVisible, c.Topmost, light)
			target := edgeControlRect(area, strip, c.EdgeSide, ww, hh)
			if expanded {
				var cursor struct{ X, Y int32 }
				u32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
				if c.EdgeSide == "left" || c.EdgeSide == "right" {
					if cursor.Y < target.Top || cursor.Y >= target.Bottom {
						target = edgeConstrainRect(winRect{target.Left, cursor.Y - hh/2, target.Right, cursor.Y + hh - hh/2}, area)
					}
				} else if cursor.X < target.Left || cursor.X >= target.Right {
					target = edgeConstrainRect(winRect{cursor.X - ww/2, target.Top, cursor.X + ww - ww/2, target.Bottom}, area)
				}
			}
			place(target)
			if ch != 0 {
				u32.NewProc("ShowWindow").Call(ch, 0)
			}
			updateBackdrop(backdrop, mh, target, c, expanded && islandVisible)
			if capsuleBackdrop != 0 {
				u32.NewProc("ShowWindow").Call(capsuleBackdrop, 0)
			}
			if expanded && islandVisible {
				u32.NewProc("ShowWindow").Call(mh, 4)
				webview2.RefreshSurface(mini)
			} else {
				u32.NewProc("ShowWindow").Call(mh, 0)
			}
			mini.Eval(fmt.Sprintf("window.setIslandExpanded && window.setIslandExpanded(%t)", expanded))
			return
		}
		edgeBar.hide()
		if islandVisible && ch != 0 {
			u32.NewProc("ShowWindow").Call(ch, 4)
		}
		// The collapsed capsule sits on the primary taskbar, immediately before
		// the notification area. The expanded panel keeps the same anchor.
		taskbar, _, _ := u32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Shell_TrayWnd"))), 0)
		var bar winRect
		if taskbar != 0 {
			u32.NewProc("GetWindowRect").Call(taskbar, uintptr(unsafe.Pointer(&bar)))
		}
		if bar.Right > bar.Left && bar.Bottom-bar.Top >= scale(24) && bar.Right-bar.Left > bar.Bottom-bar.Top && strings.HasPrefix(c.Position, "bottom") {
			anchor := bar.Right - scale(300)
			trayWindow, _, _ := u32.NewProc("FindWindowExW").Call(taskbar, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("TrayNotifyWnd"))), 0)
			var tr winRect
			if trayWindow != 0 {
				u32.NewProc("GetWindowRect").Call(trayWindow, uintptr(unsafe.Pointer(&tr)))
				if tr.Left > bar.Left {
					anchor = tr.Left - scale(8)
				}
			}
			x = anchor - ww - scale(c.OffsetX)
			if strings.Contains(c.Position, "left") {
				x = bar.Left + scale(c.OffsetX)
			}
			if c.Position == "bottom-center" {
				x = bar.Left + (bar.Right-bar.Left-ww)/2
			}
			if expanded && c.Layout != "side" {
				y = bar.Top - hh - scale(8+c.OffsetY)
			} else {
				if hh > bar.Bottom-bar.Top-scale(4) {
					hh = bar.Bottom - bar.Top - scale(4)
				}
				y = bar.Top + (bar.Bottom-bar.Top-hh)/2
			}
			if x < bar.Left {
				x = bar.Left
			}
		}
		// Anchor the 220px foreground capsule independently from popup geometry.
		baseWidth, baseHeight := scale(220), scale(40)
		baseX, baseY := area.Right-baseWidth-scale(c.OffsetX), area.Bottom-baseHeight-scale(c.OffsetY)
		if strings.Contains(c.Position, "left") {
			baseX = area.Left + scale(c.OffsetX)
		}
		if strings.HasPrefix(c.Position, "top") {
			baseY = area.Top + scale(c.OffsetY)
		}
		if c.Position == "bottom-center" {
			baseX = area.Left + (area.Right-area.Left-baseWidth)/2
		}
		if bar.Right > bar.Left && bar.Bottom-bar.Top >= scale(24) && bar.Right-bar.Left > bar.Bottom-bar.Top && strings.HasPrefix(c.Position, "bottom") {
			anchor := bar.Right - scale(300)
			child, _, _ := u32.NewProc("FindWindowExW").Call(taskbar, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("TrayNotifyWnd"))), 0)
			var notify winRect
			if child != 0 {
				u32.NewProc("GetWindowRect").Call(child, uintptr(unsafe.Pointer(&notify)))
				if notify.Left > bar.Left {
					anchor = notify.Left - scale(8)
				}
			}
			baseX = anchor - baseWidth - scale(c.OffsetX)
			if strings.Contains(c.Position, "left") {
				baseX = bar.Left + scale(c.OffsetX)
			}
			if c.Position == "bottom-center" {
				baseX = bar.Left + (bar.Right-bar.Left-baseWidth)/2
			}
			baseHeight = min(baseHeight, bar.Bottom-bar.Top-scale(2))
			baseY = bar.Top + (bar.Bottom-bar.Top-baseHeight)/2
		}
		minimumWidth := 300
		if c.Layout == "side" {
			minimumWidth = 360
		} // Four buttons, including local favorites.
		capsuleRect = capsuleViewport(baseX+baseWidth, baseY, scale(max(c.Width, minimumWidth)), baseHeight)
		if ch != 0 {
			setIslandWindowStyle(ch)
			u32.NewProc("SetWindowLongPtrW").Call(ch, signed(-20), 0x00000080)
			if capsuleRect != lastCapsuleRect {
				u32.NewProc("SetWindowPos").Call(ch, top, signed(capsuleRect.Left), signed(capsuleRect.Top), signed(capsuleRect.Right-capsuleRect.Left), signed(capsuleRect.Bottom-capsuleRect.Top), 0x0030)
				lastCapsuleRect = capsuleRect
			}
			clipCapsuleWindow(ch, capsuleRect.Right-capsuleRect.Left, capsuleRect.Bottom-capsuleRect.Top, baseWidth, c.Layout == "side" && expanded)
		}

		target := winRect{x, y, x + ww, y + hh}
		place(target)
		if (!expanded || c.Layout == "side") && ch != 0 {
			u32.NewProc("ShowWindow").Call(mh, 0)
		}
		if c.Layout == "side" {
			navRect := capsuleRect
			navRect.Right -= scale(200)
			updateBackdrop(backdrop, ch, navRect, c, false)
		} else {
			updateBackdrop(backdrop, mh, target, c, expanded && islandVisible)
		}
		baseRect := capsuleRect
		baseRect.Left = baseRect.Right - scale(220)
		baseConfig := c
		baseConfig.Radius = 22
		baseConfig.Material = "glass"
		updateBackdrop(capsuleBackdrop, ch, baseRect, baseConfig, islandVisible && c.Material != "borderless" && c.Material != "transparent")

		if capsule != nil {
			capsule.Eval(fmt.Sprintf("window.setSideExpanded && window.setSideExpanded(%t)", expanded && c.Layout == "side"))
			// Existing capsule surface stays untouched on hover; only DOM navigation changes.
		}

		mini.Eval(fmt.Sprintf("window.setIslandExpanded && window.setIslandExpanded(%t)", expanded))

		dark := uint32(1)
		if c.Theme == "light" {
			dark = 0
		}
		d32.NewProc("DwmSetWindowAttribute").Call(mh, 20, uintptr(unsafe.Pointer(&dark)), 4)
		if ch != 0 {
			d32.NewProc("DwmSetWindowAttribute").Call(ch, 20, uintptr(unsafe.Pointer(&dark)), 4)
		}

		// The WebView2 alpha mode stays fixed; only the separate native
		// backdrop window receives system blur policies.

	}
	edgeBar.track = func() (string, string) {
		a.mu.Lock()
		defer a.mu.Unlock()
		title := a.player.Track.Title
		if title == "" {
			title = "Say Music"
		}
		return title, a.player.Track.Artist
	}
	edgeBar.pinned = func() bool { return pinned }
	edgeBar.onHover = func(open bool) {
		if expanded == open {
			return
		}
		expanded = open
		apply()
	}
	edgeBar.onDrag = func(x, y int32, save bool) {
		area := edgeInsetArea(monitorArea(x, y))
		side, offset := nearestScreenEdge(area, x, y)
		// Use the strip's start rather than its midpoint for a stable drop.
		length := max(edgeBar.base.Right-edgeBar.base.Left, edgeBar.base.Bottom-edgeBar.base.Top)
		span := area.Bottom - area.Top - length
		pos := y - area.Top - length/2
		if side == "top" || side == "bottom" {
			span = area.Right - area.Left - length
			pos = x - area.Left - length/2
		}
		if span > 0 {
			offset = clamp(int(int64(pos)*10000/int64(span)), 0, 10000)
		}
		a.mu.Lock()
		c := a.cfg.Island
		c.EdgeSide = side
		c.EdgeOffset = offset
		c.EdgeMonitorX = int((area.Left + area.Right) / 2)
		c.EdgeMonitorY = int((area.Top + area.Bottom) / 2)
		a.cfg.Island = c
		a.mu.Unlock()
		expanded = false
		pinned = false
		apply()
		if save {
			_ = a.mutateConfig(func(cfg *Config) {
				cfg.Island.EdgeSide = c.EdgeSide
				cfg.Island.EdgeOffset = c.EdgeOffset
				cfg.Island.EdgeMonitorX = c.EdgeMonitorX
				cfg.Island.EdgeMonitorY = c.EdgeMonitorY
			})
		}
	}
	restore := func() {
		edgeBar.hide()
		hidden = false
		islandVisible = false
		if ch != 0 {
			u32.NewProc("ShowWindow").Call(ch, 0)
		}
		u32.NewProc("ShowWindow").Call(hwnd, 9)
		u32.NewProc("SetForegroundWindow").Call(hwnd)
		if mh != 0 {
			u32.NewProc("ShowWindow").Call(mh, 0)
			if capsuleBackdrop != 0 {
				u32.NewProc("ShowWindow").Call(capsuleBackdrop, 0)
			}
			if backdrop != 0 {
				u32.NewProc("ShowWindow").Call(backdrop, 0)
			}
		}
		removeTray()
	}
	showIsland := func() {
		if mini != nil {
			expanded = false
			pinned = false
			islandVisible = true
			u32.NewProc("ShowWindow").Call(mh, 0)
			if capsuleBackdrop != 0 {
				u32.NewProc("ShowWindow").Call(capsuleBackdrop, 0)
			}
			if backdrop != 0 {
				u32.NewProc("ShowWindow").Call(backdrop, 0)
			}
			apply()
			if a.config().Island.EdgeFree {
				return
			}
			if ch != 0 {
				u32.NewProc("ShowWindow").Call(ch, 4)
				_ = webview2.RefreshSurface(capsule)
			} else {
				u32.NewProc("ShowWindow").Call(mh, 4)
				_ = webview2.RefreshSurface(mini)
			}
		}
	}
	toTray := func() {
		addTray()
		if !trayAdded {
			return
		}
		hidden = true
		u32.NewProc("ShowWindow").Call(hwnd, 0)
		if a.config().Island.Enabled {
			showIsland()
		}
	}
	enqueue := func(action string) {
		a.mu.Lock()
		a.commandSeq++
		a.commands = append(a.commands, PlayerCommand{Seq: a.commandSeq, Action: action})
		if len(a.commands) > 64 {
			a.commands = a.commands[len(a.commands)-64:]
		}
		a.mu.Unlock()
	}
	action := func(s string) {
		switch s {
		case "tray":
			toTray()
		case "restore":
			restore()
		case "island":
			addTray()
			showIsland()
		case "hide-island":
			edgeBar.hide()
			islandVisible = false
			if ch != 0 {
				u32.NewProc("ShowWindow").Call(ch, 0)
			}
			if mh != 0 {
				u32.NewProc("ShowWindow").Call(mh, 0)
				if capsuleBackdrop != 0 {
					u32.NewProc("ShowWindow").Call(capsuleBackdrop, 0)
				}
				if backdrop != 0 {
					u32.NewProc("ShowWindow").Call(backdrop, 0)
				}
			}
		case "pin":
			pinned = true
		case "unpin":
			pinned = false
		case "expand":
			if expanded || !islandVisible {
				return
			}
			expanded = true
			apply()
			if mh != 0 && a.config().Island.Layout != "side" {
				u32.NewProc("ShowWindow").Call(mh, 4)
				_ = webview2.RefreshSurface(mini)
			}
		case "collapse", "collapse-force":
			if a.config().Island.EdgeFree && s == "collapse" {
				return
			}
			if s == "collapse-force" {
				pinned = false
			}
			if pinned || !expanded {
				return
			}
			var pt struct{ X, Y int32 }
			u32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&pt)))
			for _, handle := range []uintptr{mh, ch} {
				if handle == 0 {
					continue
				}
				if visible, _, _ := u32.NewProc("IsWindowVisible").Call(handle); visible == 0 {
					continue
				}
				if s != "collapse-force" && islandWindowContains(handle, pt.X, pt.Y) {
					return
				}
			}
			expanded = false
			apply()
		case "apply":
			apply()
			if hidden {
				if a.config().Island.Enabled {
					showIsland()
				} else if mh != 0 {
					islandVisible = false
					edgeBar.hide()
					if ch != 0 {
						u32.NewProc("ShowWindow").Call(ch, 0)
					}
					u32.NewProc("ShowWindow").Call(mh, 0)
					if capsuleBackdrop != 0 {
						u32.NewProc("ShowWindow").Call(capsuleBackdrop, 0)
					}
					if backdrop != 0 {
						u32.NewProc("ShowWindow").Call(backdrop, 0)
					}
				}
			}
		}
	}
	taskbarCreated, _, _ := u32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("TaskbarCreated"))))
	var oldMain, oldMini, oldCapsule uintptr
	mainCallback := syscall.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
		if msg == 0x1A {
			applyMainWindowTheme(h, a.config())
		}

		if msg == 0x113 && wp == 0x534e && a.config().Island.EdgeFree {
			return 0
		}
		if msg == 0x113 && wp == 0x534e {
			if islandVisible {
				overflow, _, _ := u32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("NotifyIconOverflowWindow"))), 0)
				overflowVisible, _, _ := u32.NewProc("IsWindowVisible").Call(overflow)
				if a.config().Island.Topmost || overflowVisible != 0 {
					trayRaised = true
					if ch != 0 {
						u32.NewProc("SetWindowPos").Call(ch, signed(-1), 0, 0, 0, 0, 0x0053)
					}
					if expanded && a.config().Island.Layout != "side" && mh != 0 {
						u32.NewProc("SetWindowPos").Call(mh, signed(-1), 0, 0, 0, 0, 0x0053)
					}
					if capsuleBackdrop != 0 {
						u32.NewProc("SetWindowPos").Call(capsuleBackdrop, ch, 0, 0, 0, 0, 0x0013)
					}
					if backdrop != 0 {
						panel := mh
						if a.config().Island.Layout == "side" {
							panel = ch
						}
						u32.NewProc("SetWindowPos").Call(backdrop, panel, 0, 0, 0, 0, 0x0013)
					}

				} else if trayRaised {
					trayRaised = false
					if ch != 0 {
						u32.NewProc("SetWindowPos").Call(ch, signed(-2), 0, 0, 0, 0, 0x0013)
					}
					if mh != 0 {
						u32.NewProc("SetWindowPos").Call(mh, signed(-2), 0, 0, 0, 0, 0x0013)
					}
					if capsuleBackdrop != 0 {
						u32.NewProc("SetWindowPos").Call(capsuleBackdrop, ch, 0, 0, 0, 0, 0x0013)
					}
					if backdrop != 0 {
						panel := mh
						if a.config().Island.Layout == "side" {
							panel = ch
						}
						u32.NewProc("SetWindowPos").Call(backdrop, panel, 0, 0, 0, 0, 0x0013)
					}
				}
			}
			return 0
		}
		if msg == 0x8002 {
			restore()
			return 0
		}
		if msg == taskbarCreated && hidden {
			trayAdded = false
			addTray()
			return 0
		}
		if msg == 0x10 && a.config().Island.CloseToTray {
			toTray()
			if trayAdded {
				return 0
			}
		}
		if msg == 5 && wp == 1 && (a.config().Island.Enabled || a.config().Island.CloseToTray) {
			toTray()
		}
		if msg == 0x8001 {
			if lp == 0x202 || lp == 0x203 {
				restore()
			}
			if lp == 0x205 {
				menu, _, _ := u32.NewProc("CreatePopupMenu").Call()
				defer u32.NewProc("DestroyMenu").Call(menu)
				for i, label := range []string{"Відкрити Say Music", "Показати медіаострівець", "Відтворити / пауза", "Наступний трек", "Вийти"} {
					p, _ := windows.UTF16PtrFromString(a.uiText(label))
					u32.NewProc("AppendMenuW").Call(menu, 0, uintptr(i+1), uintptr(unsafe.Pointer(p)))
				}
				var pt struct{ X, Y int32 }
				u32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&pt)))
				u32.NewProc("SetForegroundWindow").Call(hwnd)
				id, _, _ := u32.NewProc("TrackPopupMenu").Call(menu, 0x102, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
				switch id {
				case 1:
					restore()
				case 2:
					showIsland()
				case 3:
					enqueue("toggle")
				case 4:
					enqueue("next")
				case 5:
					a.requestQuit()
				}
				u32.NewProc("PostMessageW").Call(hwnd, 0, 0, 0)
			}
			return 0
		}
		ret, _, _ := u32.NewProc("CallWindowProcW").Call(oldMain, h, msg, wp, lp)
		return ret
	})
	oldMain, _, _ = u32.NewProc("SetWindowLongPtrW").Call(hwnd, signed(-4), mainCallback)
	if mh != 0 {
		miniCallback := syscall.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
			if msg == 0x14 {
				return eraseIslandBackground(h, wp)
			}
			if msg == 0x10 {
				action("collapse-force")
				return 0
			}
			ret, _, _ := u32.NewProc("CallWindowProcW").Call(oldMini, h, msg, wp, lp)
			return ret
		})
		oldMini, _, _ = u32.NewProc("SetWindowLongPtrW").Call(mh, signed(-4), miniCallback)
		configureIslandAlpha(mh)
		apply()
	}
	if ch != 0 {
		callback := syscall.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
			if msg == 0x14 {
				return eraseIslandBackground(h, wp)
			}
			if msg == 0x10 {
				action("hide-island")
				return 0
			}
			ret, _, _ := u32.NewProc("CallWindowProcW").Call(oldCapsule, h, msg, wp, lp)
			return ret
		})
		oldCapsule, _, _ = u32.NewProc("SetWindowLongPtrW").Call(ch, signed(-4), callback)
		configureIslandAlpha(ch)
	}

	u32.NewProc("SetTimer").Call(hwnd, 0x534e, 250, 0)
	a.mu.Lock()
	a.desktop = func(s string) error { w.Dispatch(func() { action(s) }); return nil }
	a.mu.Unlock()
	return func() {
		edgeBar.close()
		a.mu.Lock()
		a.desktop = nil
		a.mu.Unlock()
		removeTray()
		u32.NewProc("KillTimer").Call(hwnd, 0x534e)
		if capsuleBackdrop != 0 {
			u32.NewProc("ShowWindow").Call(capsuleBackdrop, 0)
		}
		if backdrop != 0 {
			u32.NewProc("DestroyWindow").Call(backdrop)
		}
		if capsuleBackdrop != 0 {
			u32.NewProc("DestroyWindow").Call(capsuleBackdrop)
		}
		if mh != 0 {
			u32.NewProc("SetWindowLongPtrW").Call(mh, signed(-4), oldMini)
			mini.Destroy()
		}
		if ch != 0 {
			u32.NewProc("SetWindowLongPtrW").Call(ch, signed(-4), oldCapsule)
			capsule.Destroy()
		}
		u32.NewProc("SetWindowLongPtrW").Call(hwnd, signed(-4), oldMain)
	}
}
func disableBackgroundThrottling() {
	key := "WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"
	os.Setenv(key, os.Getenv(key)+" --disable-background-timer-throttling --disable-renderer-backgrounding --disable-backgrounding-occluded-windows")
}
