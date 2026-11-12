package main

import (
	"bytes"
	"image"
	"image/draw"
	"math"
	"strings"
	"testing"
)

func TestWIPMarkerContext(t *testing.T) {
	valid := []string{
		"- [~] Active\n", "1. [~] Active\n", "- [~]\n", "- [~]\tActive\n",
		"- Parent\n  - [~] Nested\n", "- [~] **Bold** [link](https://example.com)\n",
	}
	for _, source := range valid {
		doc := listDocument(t, source, 500, 1)
		boxes := listCheckboxes(doc)
		if len(boxes) != 1 || *boxes[0].checkbox != taskActive || len(doc.dots) != 1 {
			t.Fatalf("WIP not recognized: %q", source)
		}
		if strings.Contains(sceneText(doc), "[~]") {
			t.Fatalf("marker leaked into body: %q", source)
		}
	}
	literal := []string{
		"[~] Paragraph\n", "- Before [~] text\n", "- `[~]` code\n", "- \\[~] escaped\n",
		"- **[~]** bold\n", "- [~](https://example.com)\n", "- [~]word\n", "- [~]\u3000Fullwidth space\n",
		"- [>] Alias\n", "- [/] Alias\n", "~~~\n- [~] code\n~~~\n", "    - [~] code\n",
		"- First\n\n  [~] Second paragraph\n", "- First\n  [~] Continuation\n",
		"<div>\n- [~] HTML\n</div>\n", "- <span>[~]</span>\n",
	}
	for _, source := range literal {
		doc := listDocument(t, source, 500, 1)
		if len(listCheckboxes(doc)) != 0 || len(doc.dots) != 0 {
			t.Fatalf("literal text became WIP: %q", source)
		}
	}
}
func TestWIPStatesAndFormatting(t *testing.T) {
	doc := listDocument(t, "- [ ] Alpha\n- [~] **Bravo** [L](https://example.com)\n- [X] Charlie\n", 500, 1)
	boxes := listCheckboxes(doc)
	if len(boxes) != 3 {
		t.Fatalf("checkboxes=%d", len(boxes))
	}
	for i, state := range []taskStatus{taskPending, taskActive, taskDone} {
		if *boxes[i].checkbox != state {
			t.Fatal("state lost")
		}
	}
	a, b, c := listGlyph(t, doc, "A"), listGlyph(t, doc, "B"), listGlyph(t, doc, "C")
	if a.rect.Min.X != b.rect.Min.X || b.rect.Min.X != c.rect.Min.X {
		t.Fatal("state changes shift body column")
	}
	if !b.style.bold || b.style.ink != doc.colors.fg || listGlyph(t, doc, "L").style.ink != doc.colors.accent {
		t.Fatal("active task formatting lost")
	}
	if c.style.ink != doc.colors.taskDone {
		t.Fatal("done task is no longer muted")
	}
	active := boxes[1].rect
	img := doc.paint(0, doc.height)
	if img.RGBAAt(active.Min.X, active.Min.Y+active.Dy()/2) != doc.colors.taskAccent {
		t.Fatal("active checkbox is not accent-colored")
	}
	if img.RGBAAt(active.Min.X+6, active.Min.Y+6) != doc.colors.taskActiveBg {
		t.Fatal("active checkbox hole does not match highlight")
	}
}
func TestWIPWrappingAndNesting(t *testing.T) {
	for _, width := range []int{1, 12, 48, 180, 500} {
		doc := listDocument(t, "- [~] "+strings.Repeat("word ", 20)+"\n  - [ ] Child\n", width, 1)
		if len(doc.dots) != 1 {
			t.Fatalf("width=%d: dots=%d", width, len(doc.dots))
		}
		dots := doc.dots[0].rect
		if dots.Min.X < 0 || dots.Max.X > doc.width {
			t.Fatalf("width=%d: dots overflow: %v", width, dots)
		}
		child := listGlyph(t, doc, "C")
		for _, op := range doc.ops {
			if op.radius > 0 && op.rect.Max.Y > child.rect.Min.Y {
				t.Fatalf("width=%d: parent highlight covers child", width)
			}
		}
		doc.paint(0, min(doc.height, 400))
	}
	paragraphs := listDocument(t, "- [~] Alpha\n\n  Bravo\n\n  - [ ] Child\n", 500, 1)
	if len(paragraphs.dots) != 1 || paragraphs.dots[0].rect.Min.Y < listGlyph(t, paragraphs, "B").rect.Min.Y {
		t.Fatal("dots are not after the last own paragraph")
	}
}
func TestWIPAnimationTilesMatchFullFrame(t *testing.T) {
	for _, tc := range []struct {
		width int
		scale float64
	}{{12, 0.5}, {12, 1}, {12, 4}, {300, 0.5}, {300, 1}, {300, 1.2}, {300, 4}, {301, 1}, {301, 4}} {
		width := tc.width
		doc := listDocument(t, strings.Repeat("- [~] Working **word** word word\n- [ ] Pending\n", 10), width, tc.scale)
		for _, offset := range []int{0, 1, 37} {
			base := doc.paintBase(offset, 399)
			expected := image.NewRGBA(base.Bounds())
			draw.Draw(expected, expected.Bounds(), base, image.Point{}, draw.Src)
			doc.drawDots(expected, image.Pt(0, offset), 0.25)
			actual := image.NewRGBA(base.Bounds())
			draw.Draw(actual, actual.Bounds(), base, image.Point{}, draw.Src)
			for _, tile := range doc.animationTiles(base, offset, 30, 20, 0.25) {
				x, y := tile.col*width/30, tile.row*399/20
				draw.Draw(actual, tile.image.Bounds().Add(image.Pt(x, y)), tile.image, image.Point{}, draw.Src)
			}
			if !bytes.Equal(expected.Pix, actual.Pix) {
				t.Fatalf("overlay differs: width=%d offset=%d", width, offset)
			}
			full := doc.paint(0, doc.height)
			view := doc.paint(offset, 399)
			crop := image.NewRGBA(view.Bounds())
			draw.Draw(crop, crop.Bounds(), full, image.Pt(0, offset), draw.Src)
			// Beyond the document, viewport painting deliberately keeps the
			// document background instead of transparent source-image pixels.
			if offset+399 <= doc.height && !bytes.Equal(view.Pix, crop.Pix) {
				n := 0
				for y := 0; y < view.Bounds().Dy(); y++ {
					for x := 0; x < view.Bounds().Dx(); x++ {
						if view.RGBAAt(x, y) != crop.RGBAAt(x, y) {
							if n < 12 {
								t.Logf("pixel %d,%d view=%v crop=%v", x, y, view.RGBAAt(x, y), crop.RGBAAt(x, y))
							}
							n++
						}
					}
				}
				t.Fatalf("scrolled WIP differs from full crop: width=%d offset=%d differences=%d", width, offset, n)
			}
		}
	}
}
func TestWIPAnimationVisibilityAndSize(t *testing.T) {
	doc := listDocument(t, "- [~] Active\n", 600, 1)
	if !doc.hasVisibleDots(0, 300) || doc.hasVisibleDots(doc.height+10, 300) {
		t.Fatal("visibility detection is wrong")
	}
	base := doc.paintBase(0, 300)
	tiles := doc.animationTiles(base, 0, 60, 15, 0.1)
	if len(tiles) != 1 {
		t.Fatalf("tiles=%d", len(tiles))
	}
	if tiles[0].image.Bounds().Dx()*tiles[0].image.Bounds().Dy() >= base.Bounds().Dx()*base.Bounds().Dy()/4 {
		t.Fatal("animation uploads a large viewport instead of a small patch")
	}
	first := tiles[0].image.Pix
	second := doc.animationTiles(base, 0, 60, 15, 0.4)[0].image.Pix
	if bytes.Equal(first, second) {
		t.Fatal("dots do not animate")
	}
	idle := listDocument(t, "- [ ] Pending\n- [x] Done\n", 600, 1)
	if idle.hasVisibleDots(0, 300) || len(idle.animationTiles(idle.paintBase(0, 300), 0, 60, 15, 0.1)) != 0 {
		t.Fatal("idle document animates")
	}
	if !bytes.Equal(doc.paint(0, 300).Pix, doc.paint(0, 300).Pix) {
		t.Fatal("static PNG frame is nondeterministic")
	}
}
func TestWIPDotCycle(t *testing.T) {
	for _, phase := range []float64{0, 0.1, 0.3, 0.6, 1.1} {
		for i := 0; i < 3; i++ {
			a, b := dotOpacity(phase, i), dotOpacity(phase+workingDotCycle, i)
			if a < 0.3-1e-12 || a > 0.85+1e-12 || math.Abs(a-b) > 1e-12 {
				t.Fatalf("opacity/cycle: phase=%g index=%d a=%g b=%g", phase, i, a, b)
			}
		}
	}
}
