package player

func islandWindowStyle(previous uintptr) uintptr {
	// Changing frame styles must preserve WS_VISIBLE; clearing it suppresses the
	// WebView2 child surface even when DWM still displays the host backdrop.
	return (previous & 0x10000000) | 0x80000000 | 0x02000000 | 0x04000000
}

// Side mode keeps the capsule on the taskbar; expansion adds width, not height.
func islandPanelSize(c IslandConfig, expanded bool) (int, int) {
	if !expanded {
		return 220, 40
	}
	switch c.Layout {
	case "side":
		return max(c.Width, 300), 40
	case "compact":
		return c.Width, 158
	default:
		return c.Width, c.Cover + 180
	}
}

type winRect struct{ Left, Top, Right, Bottom int32 }

// Expansion state intentionally cannot affect the host window's geometry.
func capsuleViewport(anchorRight, top, width, height int32) winRect {
	return winRect{anchorRight - width, top, anchorRight, top + height}
}
func capsuleShapeRects(width, height, baseWidth int32) (winRect, winRect) {
	base := winRect{width - baseWidth + 1, 1, width - 1, height - 1}
	nav := winRect{1, 1, width - baseWidth + baseWidth/11 - 1, height - 1}
	return base, nav
}

func edgeStripColor(c IslandConfig, accent string) string {
	if c.EdgeStandard {
		if accent != interfaceThemeColor(accent) {
			return "#ae96ff"
		}
		return interfaceThemeColor(accent)
	}
	return interfaceThemeColor(c.EdgeColor)
}
func edgeStripRect(area winRect, side string, offset, thickness, length int32) winRect {
	w, h := area.Right-area.Left, area.Bottom-area.Top
	offset = max(0, min(10000, offset))
	thickness = max(1, thickness)
	if side == "top" || side == "bottom" {
		length = min(length, w)
		x := area.Left + (w-length)*offset/10000
		y := area.Top
		if side == "bottom" {
			y = area.Bottom - thickness
		}
		return winRect{x, y, x + length, y + thickness}
	}
	length = min(length, h)
	y := area.Top + (h-length)*offset/10000
	x := area.Left
	if side != "left" {
		x = area.Right - thickness
	}
	return winRect{x, y, x + thickness, y + length}
}

// Four physical pixels remain clear on every edge, including high-DPI screens.
func edgeInsetArea(area winRect) winRect {
	return winRect{area.Left + 4, area.Top + 4, area.Right - 4, area.Bottom - 4}
}
func pointNearRect(r winRect, x, y, radius int32) bool {
	dx := max(r.Left-x, max(0, x-r.Right))
	dy := max(r.Top-y, max(0, y-r.Bottom))
	return int64(dx)*int64(dx)+int64(dy)*int64(dy) <= int64(radius)*int64(radius)
}
func edgePanelRect(area, bar winRect, side string, w, h int32) winRect {
	w = min(w, area.Right-area.Left)
	h = min(h, area.Bottom-area.Top)
	x, y := (bar.Left+bar.Right-w)/2, (bar.Top+bar.Bottom-h)/2
	switch side {
	case "left":
		x = bar.Right
	case "right":
		x = bar.Left - w
	case "top":
		y = bar.Bottom
	case "bottom":
		y = bar.Top - h
	}
	x = max(area.Left, min(x, area.Right-w))
	y = max(area.Top, min(y, area.Bottom-h))
	return winRect{x, y, x + w, y + h}
}
func nearestScreenEdge(area winRect, x, y int32) (string, int) {
	sides := []string{"left", "right", "top", "bottom"}
	dist := []int32{x - area.Left, area.Right - x, y - area.Top, area.Bottom - y}
	best := 0
	for i := 1; i < 4; i++ {
		if dist[i] < dist[best] {
			best = i
		}
	}
	span, pos := area.Bottom-area.Top, y-area.Top
	if best >= 2 {
		span = area.Right - area.Left
		pos = x - area.Left
	}
	return sides[best], clamp(int(int64(pos)*10000/int64(max(1, span))), 0, 10000)
}

// Edge strip dimensions are physical pixels. The title belongs to this same window.
func edgeWidgetRect(base winRect, side string, progress float64) winRect {
	p := max(0, min(1, progress))
	w, h := base.Right-base.Left, base.Bottom-base.Top
	if side == "left" || side == "right" {
		w += int32(220 * p)
		h += int32(float64(max(0, 48-h)) * p)
		y := (base.Top + base.Bottom - h) / 2
		if side == "left" {
			return winRect{base.Left, y, base.Left + w, y + h}
		}
		return winRect{base.Right - w, y, base.Right, y + h}
	}
	w += int32(float64(max(0, 240-w)) * p)
	h += int32(48 * p)
	x := (base.Left + base.Right - w) / 2
	if side == "top" {
		return winRect{x, base.Top, x + w, base.Top + h}
	}
	return winRect{x, base.Bottom - h, x + w, base.Bottom}
}

// Four pixels of dark plate surround the colored stripe; its configured width is unchanged.
func edgeWidgetAnchor(area winRect, side string, offset, thickness, length int32) winRect {
	r := edgeStripRect(area, side, offset, thickness+8, length+8)
	// Extend only the dark outer plate through the old screen margin.
	switch side {
	case "left":
		r.Left -= 4
	case "right":
		r.Right += 4
	case "top":
		r.Top -= 4
	case "bottom":
		r.Bottom += 4
	}
	return r
}
func edgeWidgetPill(client winRect, side string, thickness int32, base winRect, progress float64) winRect {

	w, h := client.Right, client.Bottom
	if side == "right" || side == "left" {
		length := base.Bottom - base.Top - 8
		y := (h - length) / 2
		x := int32(4)
		if side == "left" {
			x = w - thickness - 4
		}
		return winRect{x, y, x + thickness, y + length}
	}
	length := base.Right - base.Left - 8
	x := (w - length) / 2
	y := int32(4)
	if side == "top" {
		y = h - thickness - 4
	}
	return winRect{x, y, x + length, y + thickness}
}
func edgeWidgetTextRect(r winRect, side string, thickness int32) winRect {
	r.Left += 12
	r.Right -= 12
	r.Top += 4
	r.Bottom -= 4
	switch side {
	case "left":
		r.Right -= thickness
	case "right":
		r.Left += thickness
	case "top":
		r.Bottom -= thickness
	case "bottom":
		r.Top += thickness
	}
	return r
}
func edgeConstrainRect(r, area winRect) winRect {
	w, h := min(r.Right-r.Left, area.Right-area.Left), min(r.Bottom-r.Top, area.Bottom-area.Top)
	x, y := max(area.Left, min(r.Left, area.Right-w)), max(area.Top, min(r.Top, area.Bottom-h))
	return winRect{x, y, x + w, y + h}
}

// Keep the collapsed drag handle accessible, with an eight-pixel gap to controls.
func edgeControlRect(area, bar winRect, side string, w, h int32) winRect {
	anchor := bar
	switch side {
	case "left":
		anchor.Right = bar.Right + 8
	case "right":
		anchor.Left = bar.Left - 8
	case "top":
		anchor.Bottom = bar.Bottom + 8
	case "bottom":
		anchor.Top = bar.Top - 8
	}
	return edgePanelRect(area, anchor, side, w, h)
}
