//go:build windows

package player

import (
	"golang.org/x/sys/windows"
	"syscall"
	"unsafe"
)

var gdiIsland = windows.NewLazySystemDLL("gdi32.dll")

// The blur surface is a separate native window with no WebView2 children.
// Changing its composition policy cannot invalidate the browser's alpha mode.
func setIslandBackdrop(handle, panel uintptr, r winRect, c IslandConfig, visible bool) {
	if handle == 0 {
		return
	}
	blur := c.Material == "glass" || c.Material == "acrylic"
	if !visible || !blur {
		u32.NewProc("ShowWindow").Call(handle, 0)
		return
	}
	type accent struct{ State, Flags, Color, Animation uint32 }
	policy := accent{State: 3}
	type composition struct {
		Attribute  uint32
		Data, Size uintptr
	}
	data := composition{19, uintptr(unsafe.Pointer(&policy)), unsafe.Sizeof(policy)}
	proc := u32.NewProc("SetWindowCompositionAttribute")
	if proc.Find() != nil {
		u32.NewProc("ShowWindow").Call(handle, 0)
		return
	}
	// Establish the final bounds before enabling blur; cached bounds from the
	// previous layout otherwise leave a rectangular remnant under the panel.
	u32.NewProc("ShowWindow").Call(handle, 0)
	disabled := accent{}
	reset := composition{19, uintptr(unsafe.Pointer(&disabled)), unsafe.Sizeof(disabled)}
	proc.Call(handle, uintptr(unsafe.Pointer(&reset)))
	u32.NewProc("SetWindowRgn").Call(handle, 0, 0)
	u32.NewProc("SetWindowPos").Call(handle, panel, signed(r.Left), signed(r.Top), signed(r.Right-r.Left), signed(r.Bottom-r.Top), 0x0010)
	width, height := r.Right-r.Left, r.Bottom-r.Top
	dpi, _, _ := u32.NewProc("GetDpiForWindow").Call(panel)
	if dpi == 0 {
		dpi = 96
	}
	radius := int32(c.Radius) * int32(dpi) / 96
	region, _, _ := gdiIsland.NewProc("CreateRoundRectRgn").Call(1, 1, signed(width-1), signed(height-1), signed(radius*2), signed(radius*2))
	if ok, _, _ := u32.NewProc("SetWindowRgn").Call(handle, region, 1); ok == 0 && region != 0 {
		gdiIsland.NewProc("DeleteObject").Call(region)
	}
	configureIslandAlpha(handle)

	if ok, _, _ := proc.Call(handle, uintptr(unsafe.Pointer(&data))); ok == 0 {
		u32.NewProc("ShowWindow").Call(handle, 0)
		return
	}

	// Accent blur can outlive the ordinary GDI clip. Supply the rounded region
	// to DWM as well so no rectangular corner receives backdrop colorization.
	compositorRegion, _, _ := gdiIsland.NewProc("CreateRoundRectRgn").Call(1, 1, signed(width-1), signed(height-1), signed(radius*2), signed(radius*2))
	if compositorRegion != 0 {
		type blurBehind struct {
			Flags      uint32
			Enabled    int32
			Region     uintptr
			Transition int32
		}
		mask := blurBehind{Flags: 3, Enabled: 1, Region: compositorRegion}
		d32.NewProc("DwmEnableBlurBehindWindow").Call(handle, uintptr(unsafe.Pointer(&mask)))
		gdiIsland.NewProc("DeleteObject").Call(compositorRegion)
	}
	// Insert immediately behind the panel, in its same topmost/non-topmost band.
	u32.NewProc("SetWindowPos").Call(handle, panel, signed(r.Left), signed(r.Top), signed(width), signed(height), 0x0050)
}

func createIslandBackdrop() uintptr {
	type class struct {
		Size, Style                        uint32
		Proc                               uintptr
		ClassExtra, WindowExtra            int32
		Instance, Icon, Cursor, Background uintptr
		Menu, Name                         *uint16
		SmallIcon                          uintptr
	}
	instance, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	name := windows.StringToUTF16Ptr("SayMusic.MaterialBackdrop")
	callback := syscall.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
		if msg == 0x14 {
			return eraseIslandBackground(h, wp)
		} // No GDI erase brush over the compositor surface.
		if msg == 0x84 {
			return signed(-1)
		} // HTTRANSPARENT: never consume clicks.
		r, _, _ := u32.NewProc("DefWindowProcW").Call(h, msg, wp, lp)
		return r
	})
	wc := class{Proc: callback, Instance: instance, Name: name}
	wc.Size = uint32(unsafe.Sizeof(wc))
	u32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc)))
	h, _, _ := u32.NewProc("CreateWindowExW").Call(0x080000a0, uintptr(unsafe.Pointer(name)), 0, 0x80000000, 0, 0, 1, 1, 0, 0, instance, 0)
	if h != 0 {
		configureIslandAlpha(h)
	}
	return h
}

// Use per-pixel DWM composition without extending a glass frame over the client.
// An empty blur region enables alpha composition, while real blur remains in
// the dedicated background HWNDs. See GLFW's framebuffer transparency setup.
func configureIslandAlpha(handle uintptr) {
	if handle == 0 {
		return
	}
	margins := [4]int32{}
	d32.NewProc("DwmExtendFrameIntoClientArea").Call(handle, uintptr(unsafe.Pointer(&margins)))
	nonClient := uint32(1) // DWMNCRP_DISABLED: no system-painted frame.
	d32.NewProc("DwmSetWindowAttribute").Call(handle, 2, uintptr(unsafe.Pointer(&nonClient)), 4)
	region, _, _ := gdiIsland.NewProc("CreateRectRgn").Call(0, 0, signed(-1), signed(-1))
	if region != 0 {
		defer gdiIsland.NewProc("DeleteObject").Call(region)
		type blurBehind struct {
			Flags      uint32
			Enabled    int32
			Region     uintptr
			Transition int32
		}
		blur := blurBehind{Flags: 3, Enabled: 1, Region: region}
		d32.NewProc("DwmEnableBlurBehindWindow").Call(handle, uintptr(unsafe.Pointer(&blur)))
	}
	u32.NewProc("RedrawWindow").Call(handle, 0, 0, 0x0185)
}

func eraseIslandBackground(handle, dc uintptr) uintptr {
	var r winRect
	u32.NewProc("GetClientRect").Call(handle, uintptr(unsafe.Pointer(&r)))
	// BLACKNESS clears the parent redirection surface to zero, including alpha,
	// instead of leaving an undefined/white background around WebView2 children.
	gdiIsland.NewProc("PatBlt").Call(dc, 0, 0, signed(r.Right), signed(r.Bottom), 0x00000042)
	return 1
}

func clipCapsuleWindow(handle uintptr, width, height, baseWidth int32, expanded bool) {
	base, nav := capsuleShapeRects(width, height, baseWidth)
	makeRegion := func(r winRect) uintptr {
		region, _, _ := gdiIsland.NewProc("CreateRoundRectRgn").Call(signed(r.Left), signed(r.Top), signed(r.Right), signed(r.Bottom), signed(height-2), signed(height-2))
		return region
	}
	region := makeRegion(base)
	if region == 0 {
		return
	}
	if expanded {
		slider := makeRegion(nav)
		if slider != 0 {
			gdiIsland.NewProc("CombineRgn").Call(region, region, slider, 2)
			gdiIsland.NewProc("DeleteObject").Call(slider)
		}
	}
	if ok, _, _ := u32.NewProc("SetWindowRgn").Call(handle, region, 1); ok == 0 {
		gdiIsland.NewProc("DeleteObject").Call(region)
	}
}

func islandWindowContains(handle uintptr, x, y int32) bool {
	var bounds winRect
	u32.NewProc("GetWindowRect").Call(handle, uintptr(unsafe.Pointer(&bounds)))
	if x < bounds.Left || x >= bounds.Right || y < bounds.Top || y >= bounds.Bottom {
		return false
	}
	region, _, _ := gdiIsland.NewProc("CreateRectRgn").Call(0, 0, 0, 0)
	if region == 0 {
		return true
	}
	defer gdiIsland.NewProc("DeleteObject").Call(region)
	kind, _, _ := u32.NewProc("GetWindowRgn").Call(handle, region)
	if kind == 0 {
		return true
	}
	inside, _, _ := gdiIsland.NewProc("PtInRegion").Call(region, signed(x-bounds.Left), signed(y-bounds.Top))
	return inside != 0
}
