//go:build windows

package player

import (
	"golang.org/x/sys/windows"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

type edgeOverlay struct {
	hwnd             uintptr
	controlOpen      bool
	color            string
	light            bool
	reaction         bool
	reactionRadius   int32
	side             string
	thickness        int32
	base             winRect
	progress, target float64
	title, artist    string
	track            func() (string, string)
	lastMeta         time.Time
	dragging         bool
	moved, visible   bool
	downX, downY     int32
	lastDrag         time.Time
	rect             winRect
	onHover          func(bool)
	onDrag           func(int32, int32, bool)
	panel            uintptr
	pinned           func() bool
	leaveAt          time.Time
	callback         uintptr
	old              uintptr
}

func monitorArea(x, y int32) winRect {
	packed := uintptr(uint64(uint32(x)) | uint64(uint32(y))<<32)
	monitor, _, _ := u32.NewProc("MonitorFromPoint").Call(packed, 2)
	info := struct {
		Size          uint32
		Monitor, Work winRect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := u32.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
		return info.Monitor
	}
	return winRect{0, 0, 1920, 1080}
}
func newEdgeOverlay(panel uintptr) *edgeOverlay {
	e := &edgeOverlay{panel: panel, color: "#ae96ff"}
	class, _ := windows.UTF16PtrFromString("STATIC")
	e.hwnd, _, _ = u32.NewProc("CreateWindowExW").Call(0x08080088, uintptr(unsafe.Pointer(class)), 0, 0x80000100, 0, 0, 10, 90, 0, 0, 0, 0)
	if e.hwnd == 0 {
		return e
	}
	e.callback = syscall.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
		cursor := func() (int32, int32) {
			var p struct{ X, Y int32 }
			u32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&p)))
			return p.X, p.Y
		}
		switch msg {
		case 0x21:
			return 3 // MA_NOACTIVATE
		case 0x201:
			e.downX, e.downY = cursor()
			e.moved = false
			e.dragging = true
			u32.NewProc("SetCapture").Call(h)
			return 0
		case 0x200:
			if e.dragging && e.onDrag != nil {
				x, y := cursor()
				dx, dy := x-e.downX, y-e.downY
				if !e.moved && dx*dx+dy*dy < 16 {
					return 0
				}
				if time.Since(e.lastDrag) < 16*time.Millisecond {
					return 0
				}
				e.moved = true
				e.lastDrag = time.Now()
				e.onDrag(x, y, false)
			}
			return 0
		case 0x202:
			if e.dragging {
				moved := e.moved
				e.dragging = false
				u32.NewProc("ReleaseCapture").Call()
				if moved && e.onDrag != nil {
					x, y := cursor()
					e.onDrag(x, y, true)
				}
			}
			return 0
		case 0x215:
			e.dragging = false
			return 0
		case 0x113:
			visible, _, _ := u32.NewProc("IsWindowVisible").Call(h)
			if (visible == 0 && !e.controlOpen) || e.dragging {
				return 0
			}
			x, y := cursor()
			inside := func(r winRect) bool { return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom }
			overStrip := visible != 0 && inside(e.rect)
			overPanel := false
			if v, _, _ := u32.NewProc("IsWindowVisible").Call(e.panel); v != 0 {
				var r winRect
				u32.NewProc("GetWindowRect").Call(e.panel, uintptr(unsafe.Pointer(&r)))
				overPanel = inside(r)
			}
			near := e.reaction && pointNearRect(e.base, x, y, e.reactionRadius)
			if near || overStrip || overPanel {
				e.target = 1
			} else {
				e.target = 0
			}
			if !e.reaction || e.controlOpen {
				e.target = 0
			}
			if e.progress != e.target {
				delta := 0.16
				if e.progress < e.target {
					e.progress = min(e.target, e.progress+delta)
				} else {
					e.progress = max(e.target, e.progress-delta)
				}
				e.layout()
			}
			if e.track != nil && time.Since(e.lastMeta) > 250*time.Millisecond {
				e.lastMeta = time.Now()
				title, artist := e.track()
				if title != e.title || artist != e.artist {
					e.title, e.artist = title, artist
					u32.NewProc("InvalidateRect").Call(h, 0, 0)
				}
			}
			if overStrip || overPanel {
				e.leaveAt = time.Time{}
				if overStrip && !e.controlOpen && e.onHover != nil {
					if e.reaction && e.progress != 1 {
						e.progress, e.target = 1, 1
						e.layout()
					}
					e.onHover(true)
				}
			} else if e.pinned == nil || !e.pinned() {
				if e.leaveAt.IsZero() {
					e.leaveAt = time.Now()
				} else if time.Since(e.leaveAt) > 350*time.Millisecond && e.onHover != nil {
					e.onHover(false)
				}
			}
			return 0
		case 0x14:
			return 1
		case 0xF:
			ps := struct {
				DC              uintptr
				Erase           int32
				Rect            winRect
				Restore, Update int32
				Reserved        [32]byte
			}{}
			dc, _, _ := u32.NewProc("BeginPaint").Call(h, uintptr(unsafe.Pointer(&ps)))
			n, _ := strconv.ParseUint(e.color[1:], 16, 32)
			rgb := uintptr((n>>16)&255 | n&0xFF00 | (n&255)<<16)
			gdi := windows.NewLazySystemDLL("gdi32.dll")
			brush, _, _ := gdi.NewProc("CreateSolidBrush").Call(rgb)
			var r winRect
			u32.NewProc("GetClientRect").Call(h, uintptr(unsafe.Pointer(&r)))
			// The host is a translucent rounded plate; the accent pill is inset.
			bg := uintptr(0x181310) // Always dark (#101318), independent of the app theme.
			background, _, _ := gdi.NewProc("CreateSolidBrush").Call(bg)
			u32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&r)), background)
			gdi.NewProc("DeleteObject").Call(background)
			pill := edgeWidgetPill(r, e.side, e.thickness, e.base, e.progress)
			r = pill
			oldBrush, _, _ := gdi.NewProc("SelectObject").Call(dc, brush)
			nullPen, _, _ := gdi.NewProc("GetStockObject").Call(8)
			oldPen, _, _ := gdi.NewProc("SelectObject").Call(dc, nullPen)
			diameter := min(r.Right-r.Left, r.Bottom-r.Top)
			gdi.NewProc("RoundRect").Call(dc, signed(r.Left), signed(r.Top), signed(r.Right), signed(r.Bottom), signed(diameter), signed(diameter))
			gdi.NewProc("SelectObject").Call(dc, oldBrush)
			gdi.NewProc("SelectObject").Call(dc, oldPen)
			if e.progress > 0.7 && e.reaction {
				textRect := edgeWidgetTextRect(winRect{0, 0, e.rect.Right - e.rect.Left, e.rect.Bottom - e.rect.Top}, e.side, e.thickness)
				gdi.NewProc("SetBkMode").Call(dc, 1)
				fg := uintptr(0xFAFAFA)
				muted := uintptr(0xB8B8B8)
				draw := func(value string, y, height int32, color uintptr, weight int32) {
					face, _ := windows.UTF16PtrFromString("Segoe UI")
					font, _, _ := gdi.NewProc("CreateFontW").Call(signed(-13), 0, 0, 0, signed(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
					old, _, _ := gdi.NewProc("SelectObject").Call(dc, font)
					gdi.NewProc("SetTextColor").Call(dc, color)
					tr := winRect{textRect.Left, y, textRect.Right, y + height}
					t, _ := windows.UTF16PtrFromString(value)
					u32.NewProc("DrawTextW").Call(dc, uintptr(unsafe.Pointer(t)), signed(-1), uintptr(unsafe.Pointer(&tr)), 0x8824)
					gdi.NewProc("SelectObject").Call(dc, old)
					gdi.NewProc("DeleteObject").Call(font)
				}
				mid := (textRect.Top + textRect.Bottom) / 2
				draw(e.title, mid-18, 20, fg, 600)
				draw(e.artist, mid+2, 18, muted, 400)
			}
			gdi.NewProc("DeleteObject").Call(brush)
			u32.NewProc("EndPaint").Call(h, uintptr(unsafe.Pointer(&ps)))
			return 0
		}
		r, _, _ := u32.NewProc("CallWindowProcW").Call(e.old, h, msg, wp, lp)
		return r
	})
	e.old, _, _ = u32.NewProc("SetWindowLongPtrW").Call(e.hwnd, signed(-4), e.callback)
	return e
}
func (e *edgeOverlay) place(r winRect, color string, visible, topmost, light bool) {
	if e.hwnd == 0 {
		return
	}
	e.base = r
	e.rect = edgeWidgetRect(e.base, e.side, e.progress)
	if visible != e.visible {
		e.visible = visible
		if visible {
			u32.NewProc("SetTimer").Call(e.hwnd, 1, 16, 0)
		} else {
			u32.NewProc("KillTimer").Call(e.hwnd, 1)
		}
	}
	if e.color != color || e.light != light {
		e.light = light
		e.color = color
		u32.NewProc("InvalidateRect").Call(e.hwnd, 0, 1)
	}
	top := signed(-2)
	if topmost {
		top = signed(-1)
	}
	flags := uintptr(0x0010)
	if visible {
		flags |= 0x0040
	} else {
		flags |= 0x0080
	}
	u32.NewProc("SetWindowPos").Call(e.hwnd, top, signed(e.rect.Left), signed(e.rect.Top), signed(e.rect.Right-e.rect.Left), signed(e.rect.Bottom-e.rect.Top), flags)
	e.layout()
}
func (e *edgeOverlay) layout() {
	e.rect = edgeConstrainRect(edgeWidgetRect(e.base, e.side, e.progress), monitorArea((e.base.Left+e.base.Right)/2, (e.base.Top+e.base.Bottom)/2))
	r := e.rect
	u32.NewProc("SetWindowPos").Call(e.hwnd, 0, signed(r.Left), signed(r.Top), signed(r.Right-r.Left), signed(r.Bottom-r.Top), 0x0014)
	u32.NewProc("SetLayeredWindowAttributes").Call(e.hwnd, 0, 200, 2)
	diameter := min(r.Right-r.Left, r.Bottom-r.Top)
	if e.progress > 0 {
		diameter = min(diameter, 20)
	}
	gdi := windows.NewLazySystemDLL("gdi32.dll")
	region, _, _ := gdi.NewProc("CreateRoundRectRgn").Call(0, 0, signed(r.Right-r.Left+1), signed(r.Bottom-r.Top+1), signed(diameter), signed(diameter))
	if ok, _, _ := u32.NewProc("SetWindowRgn").Call(e.hwnd, region, 1); ok == 0 && region != 0 {
		gdi.NewProc("DeleteObject").Call(region)
	}
	u32.NewProc("InvalidateRect").Call(e.hwnd, 0, 0)
}

func (e *edgeOverlay) hide() {
	e.progress, e.target = 0, 0
	e.visible = false
	if e.hwnd != 0 {
		u32.NewProc("KillTimer").Call(e.hwnd, 1)
		u32.NewProc("ShowWindow").Call(e.hwnd, 0)
	}
}
func (e *edgeOverlay) close() {
	if e.hwnd != 0 {
		u32.NewProc("KillTimer").Call(e.hwnd, 1)
		u32.NewProc("SetWindowLongPtrW").Call(e.hwnd, signed(-4), e.old)
		u32.NewProc("DestroyWindow").Call(e.hwnd)
	}
}
