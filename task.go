package main

import (
	"image"
	"image/color"
	"math"

	"github.com/yuin/goldmark/ast"
	"golang.org/x/image/vector"
)

// TODO geometry and colors follow rexpad. See THIRD_PARTY_NOTICES.md.
// The surrounding Markdown margins and nesting remain the viewer's layout.
type taskMetrics struct{ font, box, gap, pitch, line, baseline int }

func taskLayout(scale float64) taskMetrics {
	px := func(points float64) int { return max(1, int(math.Round(points*scale))) }
	return taskMetrics{font: px(14), box: px(12), gap: px(6.5), pitch: px(28), line: px(18), baseline: px(19)}
}
func (l *layout) taskScale() float64 {
	if l.scale > 0 {
		return l.scale
	}
	return max(1.0/16, float64(l.size)/16)
}
func (l *layout) taskParagraph(block ast.Node, left, right int, checked, first bool) {
	m := taskLayout(l.taskScale())
	st := l.normal()
	st.size, st.baseline, st.lineHeight = m.font, m.baseline, m.line
	parts := l.inlineParts(block, st)
	if first && len(parts) > 0 && parts[0].checkbox != nil {
		parts = parts[1:]
	}
	if checked {
		for i := range parts {
			parts[i].style.ink = l.doc.colors.taskDone
		}
	}
	l.inlineWithLineHeight(parts, left, right, 0, m.line)
	l.y += max(0, m.pitch-m.line)
}
func (l *layout) drawCheckbox(x, y, size int, checked bool) {
	l.addOp(paintOp{rect: image.Rect(x, y, x+size, y+size), checkbox: &checked})
}

type checkboxKey struct {
	size    int
	checked bool
}

func checkboxImage(size int, checked bool, colors palette) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	scale := float32(size) / 12
	if checked {
		fillRoundedRect(img, 0, 0, float32(size), float32(size), 3*scale, colors.taskAccent)
		// Draw the check as geometry so it does not depend on font coverage.
		points := [][2]float32{{3, 6.2}, {5.1, 8.3}, {9, 3.8}}
		for i := 0; i+1 < len(points); i++ {
			a, b := points[i], points[i+1]
			dx, dy := b[0]-a[0], b[1]-a[1]
			length := float32(math.Hypot(float64(dx), float64(dy)))
			nx, ny := -dy/length*0.75, dx/length*0.75
			ex, ey := dx/length*0.45, dy/length*0.45
			path := vector.NewRasterizer(size, size)
			path.MoveTo((a[0]-nx-ex)*scale, (a[1]-ny-ey)*scale)
			path.LineTo((b[0]-nx+ex)*scale, (b[1]-ny+ey)*scale)
			path.LineTo((b[0]+nx+ex)*scale, (b[1]+ny+ey)*scale)
			path.LineTo((a[0]+nx-ex)*scale, (a[1]+ny-ey)*scale)
			path.ClosePath()
			path.Draw(img, img.Bounds(), image.NewUniform(colors.taskCheck), image.Point{})
		}
	} else {
		fillRoundedRect(img, 0, 0, float32(size), float32(size), 3*scale, colors.taskFrame)
		fillRoundedRect(img, scale, scale, float32(size)-scale, float32(size)-scale, 2*scale, colors.bg)
	}
	return img
}

// Rasterize only the small checkbox, never a viewport-sized mask per item.
func fillRoundedRect(dst *image.RGBA, x0, y0, x1, y1, r float32, ink color.RGBA) {
	r = min(r, (x1-x0)/2, (y1-y0)/2)
	const k = float32(0.55228475)
	p := vector.NewRasterizer(dst.Bounds().Dx(), dst.Bounds().Dy())
	p.MoveTo(x0+r, y0)
	p.LineTo(x1-r, y0)
	p.CubeTo(x1-r+k*r, y0, x1, y0+r-k*r, x1, y0+r)
	p.LineTo(x1, y1-r)
	p.CubeTo(x1, y1-r+k*r, x1-r+k*r, y1, x1-r, y1)
	p.LineTo(x0+r, y1)
	p.CubeTo(x0+r-k*r, y1, x0, y1-r+k*r, x0, y1-r)
	p.LineTo(x0, y0+r)
	p.CubeTo(x0, y0+r-k*r, x0+r-k*r, y0, x0+r, y0)
	p.ClosePath()
	p.Draw(dst, dst.Bounds(), image.NewUniform(ink), image.Point{})
}

func luminance(c color.RGBA) float64 {
	linear := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}
func (p palette) withAccent(accent color.RGBA) palette {
	p.accent, p.taskAccent = accent, accent
	p.taskCheck = color.RGBA{255, 255, 255, 255}
	if luminance(accent) > 0.45 {
		p.taskCheck = p.bg
	}
	return p
}
