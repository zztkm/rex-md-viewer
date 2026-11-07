package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/yuin/goldmark/ast"
	ext "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

type taskStatus uint8

const (
	taskNone taskStatus = iota
	taskPending
	taskActive
	taskDone
)

// Match the context restrictions of goldmark's TaskList parser, adding only
// [~]. Standard pending/done markers continue to use goldmark's implementation.
type wipParser struct{}

func (*wipParser) Trigger() []byte { return []byte{'['} }
func (*wipParser) Parse(parent ast.Node, reader text.Reader, _ parser.Context) ast.Node {
	if parent.Parent() == nil || parent.Parent().FirstChild() != parent || parent.HasChildren() {
		return nil
	}
	if _, ok := parent.Parent().(*ast.ListItem); !ok {
		return nil
	}
	line, _ := reader.PeekLine()
	if !bytes.HasPrefix(line, []byte("[~]")) {
		return nil
	}
	if len(line) > 3 && line[3] != ' ' && line[3] != '\t' && line[3] != '\n' && line[3] != '\r' {
		return nil
	}
	end := 3
	for end < len(line) && (line[end] == ' ' || line[end] == '\t') {
		end++
	}
	reader.Advance(end)
	return &wipMarker{}
}

type wipMarker struct{ ast.BaseInline }

var kindWIPMarker = ast.NewNodeKind("WIPMarker")

func (*wipMarker) Kind() ast.NodeKind { return kindWIPMarker }
func (n *wipMarker) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"State": "active"}, nil)
}

func itemTaskState(item ast.Node) taskStatus {
	first := item.FirstChild()
	if first == nil {
		return taskNone
	}
	switch n := first.FirstChild().(type) {
	case *ext.TaskCheckBox:
		if n.IsChecked {
			return taskDone
		}
		return taskPending
	case *wipMarker:
		return taskActive
	}
	return taskNone
}

// Working-dot geometry and easing follow rexpad's dots.go.
const (
	workingDotDiameter = 3.5
	workingDotSpacing  = 2.0
	workingDotsWidth   = 3*workingDotDiameter + 2*workingDotSpacing
	workingDotsGap     = 6.0
	workingDotCycle    = 1.2
	workingDotStagger  = 0.2
	workingDotsFPS     = 15
	staticDotsPhase    = workingDotStagger + workingDotCycle/4
)

type workingDots struct {
	rect  image.Rectangle
	scale float64
}

func dotOpacity(phase float64, index int) float64 {
	t := math.Mod((phase-float64(index)*workingDotStagger)/workingDotCycle, 1)
	if t < 0 {
		t++
	}
	ease := func(x float64) float64 { return x * x * (3 - 2*x) }
	if t < 0.25 {
		return 0.3 + 0.55*ease(t/0.25)
	}
	if t < 0.5 {
		return 0.85 - 0.55*ease((t-0.25)/0.25)
	}
	return 0.3
}
func dotsImage(scale, phase float64, colors palette) *image.RGBA {
	w, h := max(1, int(math.Ceil(workingDotsWidth*scale))), max(1, int(math.Ceil(workingDotDiameter*scale)))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	diameter := float32(workingDotDiameter * scale)
	for i := 0; i < 3; i++ {
		x := float32(float64(i) * (workingDotDiameter + workingDotSpacing) * scale)
		ink := mix(colors.fg, colors.taskActiveBg, 100-int(math.Round(dotOpacity(phase, i)*100)))
		fillRoundedRect(img, x, 0, x+diameter, diameter, diameter/2, ink)
	}
	return img
}
func (d *document) drawDots(dst *image.RGBA, origin image.Point, phase float64) {
	cache := make(map[float64]*image.RGBA)
	for _, dots := range d.dots {
		r := dots.rect.Sub(origin)
		if !r.Overlaps(dst.Bounds()) {
			continue
		}
		img := cache[dots.scale]
		if img == nil {
			img = dotsImage(dots.scale, phase, d.colors)
			cache[dots.scale] = img
		}
		draw.Draw(dst, r, img, image.Point{}, draw.Over)
	}
}
func (d *document) hasVisibleDots(offset, height int) bool {
	view := image.Rect(0, offset, d.width, offset+height)
	for _, dots := range d.dots {
		if dots.rect.Overlaps(view) {
			return true
		}
	}
	return false
}

func (l *layout) startActiveRow(left, right int) int {
	scale := l.taskScale()
	inset, pad := max(1, int(math.Round(6*scale))), max(1, int(math.Round(2*scale)))
	index := len(l.doc.ops)
	if !l.addOp(paintOp{rect: image.Rect(max(0, left-inset), l.y+pad, min(l.doc.width, right+inset), l.y+pad), ink: l.doc.colors.taskActiveBg, radius: 6 * scale}) {
		return -1
	}
	return index
}
func (l *layout) finishActiveRow(index int) {
	if index < 0 {
		return
	}
	op := &l.doc.ops[index]
	op.rect.Max.Y = max(op.rect.Min.Y+1, l.y-max(1, int(math.Round(2*l.taskScale()))))
}

// Rasterize fixed-size corners separately from the solid center. This avoids
// viewport-sized masks and keeps corner antialiasing identical when scrolled.
func paintRoundedRect(dst *image.RGBA, r image.Rectangle, radius float64, ink color.RGBA) {
	if r.Intersect(dst.Bounds()).Empty() {
		return
	}
	radius = min(radius, float64(r.Dx())/2, float64(r.Dy())/2)
	if radius <= 0 {
		draw.Draw(dst, r, image.NewUniform(ink), image.Point{}, draw.Over)
		return
	}
	side := max(1, int(math.Ceil(radius)))
	patch := image.NewRGBA(image.Rect(0, 0, side*2, side*2))
	fillRoundedRect(patch, 0, 0, float32(side*2), float32(side*2), float32(radius), ink)
	left, top := min(side, r.Dx()/2), min(side, r.Dy()/2)
	right, bottom := min(side, r.Dx()-left), min(side, r.Dy()-top)
	x1, x2, y1, y2 := r.Min.X+left, r.Max.X-right, r.Min.Y+top, r.Max.Y-bottom
	fill := image.NewUniform(ink)
	for _, rect := range []image.Rectangle{{Min: image.Pt(r.Min.X, y1), Max: image.Pt(r.Max.X, y2)}, {Min: image.Pt(x1, r.Min.Y), Max: image.Pt(x2, y1)}, {Min: image.Pt(x1, y2), Max: image.Pt(x2, r.Max.Y)}} {
		draw.Draw(dst, rect, fill, image.Point{}, draw.Over)
	}
	for _, corner := range []struct {
		rect   image.Rectangle
		source image.Point
	}{
		{image.Rectangle{Min: r.Min, Max: image.Pt(x1, y1)}, image.Point{}},
		{image.Rectangle{Min: image.Pt(x2, r.Min.Y), Max: image.Pt(r.Max.X, y1)}, image.Pt(side*2-right, 0)},
		{image.Rectangle{Min: image.Pt(r.Min.X, y2), Max: image.Pt(x1, r.Max.Y)}, image.Pt(0, side*2-bottom)},
		{image.Rectangle{Min: image.Pt(x2, y2), Max: r.Max}, image.Pt(side*2-right, side*2-bottom)},
	} {
		draw.Draw(dst, corner.rect, patch, corner.source, draw.Over)
	}
}

// Small, cell-aligned overlays let the terminal animate dots without uploading
// or laying out the entire document again. Overlapping patches are merged.
type animationTile struct {
	image                *image.RGBA
	row, col, cols, rows int // zero-based relative to the body image
}

func (d *document) animationTiles(base *image.RGBA, offset, cols, rows int, phase float64) []animationTile {
	if base == nil || cols < 1 || rows < 1 {
		return nil
	}
	w, h := base.Bounds().Dx(), base.Bounds().Dy()
	if w < 1 || h < 1 {
		return nil
	}
	var cells []image.Rectangle
	for _, dots := range d.dots {
		visible := dots.rect.Sub(image.Pt(0, offset)).Intersect(base.Bounds())
		if visible.Empty() {
			continue
		}
		r := image.Rect(visible.Min.X*cols/w, visible.Min.Y*rows/h, (visible.Max.X*cols+w-1)/w, (visible.Max.Y*rows+h-1)/h)
		for i := 0; i < len(cells); {
			if r.Overlaps(cells[i]) {
				r = r.Union(cells[i])
				cells = append(cells[:i], cells[i+1:]...)
				i = 0
			} else {
				i++
			}
		}
		cells = append(cells, r)
	}
	var tiles []animationTile
	for _, cell := range cells {
		r := image.Rect(cell.Min.X*w/cols, cell.Min.Y*h/rows, cell.Max.X*w/cols, cell.Max.Y*h/rows)
		if r.Empty() {
			continue
		}
		patch := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(patch, patch.Bounds(), base, r.Min, draw.Src)
		d.drawDots(patch, image.Pt(r.Min.X, offset+r.Min.Y), phase)
		tiles = append(tiles, animationTile{image: patch, row: cell.Min.Y, col: cell.Min.X, cols: cell.Dx(), rows: cell.Dy()})
	}
	return tiles
}
