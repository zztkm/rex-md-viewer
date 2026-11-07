package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"
)

func TestTaskMetrics(t *testing.T) {
	for _, tc := range []struct {
		scale float64
		want  taskMetrics
	}{
		{1, taskMetrics{font: 14, box: 12, gap: 7, pitch: 28, line: 18, baseline: 19}},
		{2, taskMetrics{font: 28, box: 24, gap: 13, pitch: 56, line: 36, baseline: 38}},
	} {
		if got := taskLayout(tc.scale); got != tc.want {
			t.Fatalf("scale=%g: got=%+v want=%+v", tc.scale, got, tc.want)
		}
	}
}
func TestTaskRowStyleAndPitch(t *testing.T) {
	doc := listDocument(t, "- [ ] Alpha\n- [x] Bravo\n", 600, 1)
	a, b := listGlyph(t, doc, "A"), listGlyph(t, doc, "B")
	if a.style.size != 14 || a.style.baseline != 19 || a.style.lineHeight != 18 {
		t.Fatalf("task typography: %+v", a.style)
	}
	if a.style.ink != doc.colors.fg || b.style.ink != doc.colors.taskDone {
		t.Fatal("task state does not set text color")
	}
	if b.style.strike {
		t.Fatal("completion must not add strikethrough")
	}
	if pitch := b.rect.Min.Y - a.rect.Min.Y; pitch != 28 {
		t.Fatalf("pitch=%d", pitch)
	}
	boxes := listCheckboxes(doc)
	if len(boxes) != 2 || *boxes[0].checkbox != taskPending || *boxes[1].checkbox != taskDone {
		t.Fatal("checkbox states lost")
	}
	for i, glyph := range []paintOp{a, b} {
		box := boxes[i].rect
		if box.Dx() != 12 || box.Dy() != 12 || box.Min.Y-glyph.rect.Min.Y != 8 {
			t.Fatalf("box geometry: %v", box)
		}
		if glyph.rect.Min.X-box.Max.X != 7 {
			t.Fatal("task gap is not rasterized from 6.5 points")
		}
	}
}
func TestTaskWrappedLinePitchAndNormalListUnchanged(t *testing.T) {
	doc := listDocument(t, "- [ ] "+strings.Repeat("word ", 24)+"\n", 180, 1)
	rows := []int{}
	for _, op := range doc.ops {
		if op.text == "w" && (len(rows) == 0 || rows[len(rows)-1] != op.rect.Min.Y) {
			rows = append(rows, op.rect.Min.Y)
		}
	}
	if len(rows) < 3 {
		t.Fatal("task did not wrap")
	}
	for i := 1; i < len(rows); i++ {
		if rows[i]-rows[i-1] != 18 {
			t.Fatalf("task line pitch=%d", rows[i]-rows[i-1])
		}
	}
	normal := listDocument(t, "- Alpha\n- Bravo\n", 600, 1)
	a, b := listGlyph(t, normal, "A"), listGlyph(t, normal, "B")
	if a.style.size != 16 || a.style.baseline != 0 || b.rect.Min.Y-a.rect.Min.Y != 24 {
		t.Fatal("normal list typography changed")
	}
}
func TestTaskCompletionPreservesMarkdownAndChildStates(t *testing.T) {
	doc := listDocument(t, "- [x] **A** [L](https://example.com) `C` ~~S~~ plain\n  - [ ] Nested\n", 600, 1)
	if !listGlyph(t, doc, "A").style.bold || !listGlyph(t, doc, "L").style.underline || !listGlyph(t, doc, "C").style.code || !listGlyph(t, doc, "S").style.strike {
		t.Fatal("inline Markdown lost")
	}
	for _, s := range []string{"A", "L", "C", "S", "p"} {
		if listGlyph(t, doc, s).style.ink != doc.colors.taskDone {
			t.Fatalf("%q did not inherit done color", s)
		}
	}
	if listGlyph(t, doc, "N").style.ink != doc.colors.fg {
		t.Fatal("done parent muted a pending child")
	}
}
func TestTaskCheckboxRaster(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		pal := paletteFor(theme)
		fonts, err := loadFonts("", "")
		if err != nil {
			t.Fatal(err)
		}
		doc, err := layoutMarkdown([]byte("- [ ] Alpha\n- [x] Bravo\n"), "task.md", 300, 1, fonts, pal)
		if err != nil {
			fonts.close()
			t.Fatal(err)
		}
		img := doc.paint(0, doc.height)
		boxes := listCheckboxes(doc)
		for _, box := range boxes {
			r := box.rect
			corner := img.RGBAAt(r.Min.X, r.Min.Y)
			edge := img.RGBAAt(r.Min.X, r.Min.Y+r.Dy()/2)
			if corner == edge {
				t.Fatal("checkbox is square instead of rounded")
			}
		}
		pending := boxes[0].rect
		if img.RGBAAt(pending.Min.X+6, pending.Min.Y+6) != pal.bg {
			t.Fatal("pending checkbox is not hollow")
		}
		if img.RGBAAt(pending.Min.X, pending.Min.Y+6) != pal.taskFrame {
			t.Fatal("pending frame color is not neutral")
		}
		done := boxes[1].rect
		foundCheck := false
		for y := done.Min.Y + 3; y < done.Max.Y-2; y++ {
			for x := done.Min.X + 2; x < done.Max.X-2; x++ {
				if img.RGBAAt(x, y) == pal.taskCheck {
					foundCheck = true
				}
			}
		}
		if !foundCheck {
			t.Fatal("completed checkbox has no contrasting checkmark")
		}
		fonts.close()
	}
}
func TestTaskViewportMatchesFullCrop(t *testing.T) {
	for _, width := range []int{300, 12} {
		source := strings.Repeat("- [ ] Pending word word word\n- [x] Completed word word\n", 30)
		if width == 12 {
			source = "- [ ] `code words` [link text](https://example.com)\n- [x] completed\n"
		}
		doc := listDocument(t, source, width, 1)
		full := doc.paint(0, doc.height)
		view := doc.paint(37, 240)
		crop := image.NewRGBA(view.Bounds())
		draw.Draw(crop, crop.Bounds(), full, image.Pt(0, 37), draw.Src)
		if !bytes.Equal(view.Pix, crop.Pix) {
			t.Fatalf("checkbox/text clipping differs at width %d", width)
		}
	}
}
func TestTaskUppercaseCompletion(t *testing.T) {
	doc := listDocument(t, "- [X] Alpha\n", 300, 1)
	boxes := listCheckboxes(doc)
	if len(boxes) != 1 || *boxes[0].checkbox != taskDone || listGlyph(t, doc, "A").style.ink != doc.colors.taskDone {
		t.Fatal("uppercase completion marker not styled")
	}
}

func TestTaskPaletteAccentAndContrast(t *testing.T) {
	base := makePalette(color.RGBA{20, 20, 20, 255}, color.RGBA{230, 230, 230, 255})
	blue := color.RGBA{40, 90, 180, 255}
	pal := base.withAccent(blue)
	if pal.taskAccent != blue || pal.accent != blue || pal.taskCheck != (color.RGBA{255, 255, 255, 255}) {
		t.Fatal("theme accent not applied")
	}
	bright := base.withAccent(color.RGBA{255, 255, 0, 255})
	if bright.taskCheck != base.bg {
		t.Fatal("bright accent loses checkmark contrast")
	}
	if pal.taskDone == pal.fg || pal.taskFrame == blue {
		t.Fatal("neutral task colors are not distinct")
	}
}
