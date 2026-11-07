package main

import (
	"strings"
	"testing"
	"unicode"
)

func listDocument(t *testing.T, source string, width int, scale float64) *document {
	t.Helper()
	fonts, err := loadFonts("", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fonts.close)
	doc, err := layoutMarkdown([]byte(source), "list.md", width, scale, fonts, paletteFor("light"))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func listGlyph(t *testing.T, doc *document, text string) paintOp {
	t.Helper()
	for _, op := range doc.ops {
		if op.text == text {
			return op
		}
	}
	t.Fatalf("glyph %q not found", text)
	return paintOp{}
}
func listCheckboxes(doc *document) []paintOp {
	var boxes []paintOp
	for _, op := range doc.ops {
		if op.checkbox != nil {
			boxes = append(boxes, op)
		}
	}
	return boxes
}
func TestListBulletGap(t *testing.T) {
	for _, scale := range []float64{1, 2} {
		doc := listDocument(t, "P\n\n- A\n", 600, scale)
		paragraph, bullet, body := listGlyph(t, doc, "P"), listGlyph(t, doc, "•"), listGlyph(t, doc, "A")
		if bullet.rect.Min.X != paragraph.rect.Min.X {
			t.Fatal("bullet is indented from the paragraph edge")
		}
		if gap := body.rect.Min.X - bullet.rect.Max.X; gap != int(8*scale) {
			t.Fatalf("scale=%g: marker gap=%d", scale, gap)
		}
	}
}
func TestListTaskMarkerAndNesting(t *testing.T) {
	doc := listDocument(t, "P\n\n- [ ] A\n  - [ ] B\n- [x] C\n", 600, 1)
	boxes := listCheckboxes(doc)
	if len(boxes) != 3 {
		t.Fatalf("checkboxes=%d", len(boxes))
	}
	paragraph, first, nested, last := listGlyph(t, doc, "P"), listGlyph(t, doc, "A"), listGlyph(t, doc, "B"), listGlyph(t, doc, "C")
	if boxes[0].rect.Min.X != paragraph.rect.Min.X || boxes[2].rect.Min.X != paragraph.rect.Min.X {
		t.Fatal("top-level checkbox has an unused bullet indent")
	}
	if boxes[1].rect.Min.X != first.rect.Min.X {
		t.Fatal("nested marker does not begin at the parent text column")
	}
	for i, body := range []paintOp{first, nested, last} {
		if gap := body.rect.Min.X - boxes[i].rect.Max.X; gap != 7 {
			t.Fatalf("checkbox %d gap=%d", i, gap)
		}
	}
	if first.rect.Min.X != last.rect.Min.X {
		t.Fatal("task body columns differ")
	}
}
func TestListMixedMarkersAlign(t *testing.T) {
	doc := listDocument(t, "P\n\n- A\n- [ ] B\n- [x] C\n", 600, 1)
	bullet, paragraph := listGlyph(t, doc, "•"), listGlyph(t, doc, "P")
	boxes := listCheckboxes(doc)
	if len(boxes) != 2 {
		t.Fatalf("checkboxes=%d", len(boxes))
	}
	if bullet.rect.Min.X != paragraph.rect.Min.X || boxes[0].rect.Min.X != bullet.rect.Min.X {
		t.Fatal("mixed marker left edges differ")
	}
	x := listGlyph(t, doc, "A").rect.Min.X
	if listGlyph(t, doc, "B").rect.Min.X != x || listGlyph(t, doc, "C").rect.Min.X != x {
		t.Fatal("mixed list body columns differ")
	}
}
func TestListOrderedMarkerWidthAndAlignment(t *testing.T) {
	for _, source := range []string{"9. A\n10. B\n", "9999. A\n10000. B\n"} {
		doc := listDocument(t, source, 600, 1)
		a, b := listGlyph(t, doc, "A"), listGlyph(t, doc, "B")
		if a.rect.Min.X != b.rect.Min.X {
			t.Fatal("ordered body columns differ")
		}
		var markerRows []int
		var rowText = map[int]string{}
		for _, op := range doc.ops {
			if op.text != "" && (unicode.IsDigit([]rune(op.text)[0]) || op.text == ".") {
				y := op.rect.Min.Y
				if _, ok := rowText[y]; !ok {
					markerRows = append(markerRows, y)
				}
				rowText[y] += op.text
				if op.text == "." && a.rect.Min.X-op.rect.Max.X != 8 {
					t.Fatalf("number-to-body gap=%d", a.rect.Min.X-op.rect.Max.X)
				}
			}
		}
		if len(markerRows) != 2 || markerRows[0] != a.rect.Min.Y || markerRows[1] != b.rect.Min.Y {
			t.Fatalf("number wraps independently: rows=%v body=%d,%d", markerRows, a.rect.Min.Y, b.rect.Min.Y)
		}
		lines := strings.Split(strings.TrimSpace(source), "\n")
		for i, line := range lines {
			want := strings.SplitN(line, " ", 2)[0]
			if got := rowText[markerRows[i]]; got != want {
				t.Fatalf("marker=%q want=%q", got, want)
			}
		}
	}
}
func TestListContinuationLinesAlignWithBody(t *testing.T) {
	for _, prefix := range []string{"- ", "1. ", "- [ ] ", "- [x] "} {
		doc := listDocument(t, prefix+strings.Repeat("word ", 20)+"\n", 180, 1)
		first := listGlyph(t, doc, "w")
		rows := map[int]int{}
		for _, op := range doc.ops {
			if op.text == "w" {
				if _, ok := rows[op.rect.Min.Y]; !ok {
					rows[op.rect.Min.Y] = op.rect.Min.X
				}
			}
		}
		if len(rows) < 2 {
			t.Fatalf("%q did not wrap", prefix)
		}
		for y, x := range rows {
			if x != first.rect.Min.X {
				t.Fatalf("%q: row y=%d starts at %d, want %d", prefix, y, x, first.rect.Min.X)
			}
		}
	}
}
func TestListTaskBlocksAndFormatting(t *testing.T) {
	doc := listDocument(t, "- [ ] **Alpha**  \n  Bravo\n\n  Charlie\n\n- [x] Delta\n", 600, 1)
	boxes := listCheckboxes(doc)
	if len(boxes) != 2 {
		t.Fatalf("checkboxes=%d", len(boxes))
	}
	x := listGlyph(t, doc, "A").rect.Min.X
	for _, text := range []string{"B", "C", "D"} {
		if listGlyph(t, doc, text).rect.Min.X != x {
			t.Fatalf("%q does not align with task text", text)
		}
	}
	if !listGlyph(t, doc, "A").style.bold {
		t.Fatal("task inline formatting lost")
	}
}
func TestListEmptyItemsReserveSeparateRows(t *testing.T) {
	doc := listDocument(t, "-\n-\n", 600, 1)
	rows := map[int]bool{}
	count := 0
	for _, op := range doc.ops {
		if op.text == "•" {
			rows[op.rect.Min.Y] = true
			count++
		}
	}
	if count != 2 || len(rows) != 2 {
		t.Fatalf("empty list items overlap: markers=%d rows=%d", count, len(rows))
	}
}
func TestListCodeIndentationIsPreserved(t *testing.T) {
	doc := listDocument(t, "- [ ] A\n\n  ~~~\n      keep\n  ~~~\n", 600, 1)
	if !strings.Contains(sceneText(doc), "    keep") {
		t.Fatalf("code indentation lost: %q", sceneText(doc))
	}
}
func TestListTaskGapScales(t *testing.T) {
	doc := listDocument(t, "P\n\n- [ ] A\n", 1000, 2)
	boxes := listCheckboxes(doc)
	if len(boxes) != 1 {
		t.Fatalf("checkboxes=%d", len(boxes))
	}
	if boxes[0].rect.Min.X != listGlyph(t, doc, "P").rect.Min.X {
		t.Fatal("scaled task marker is indented")
	}
	if gap := listGlyph(t, doc, "A").rect.Min.X - boxes[0].rect.Max.X; gap != 13 {
		t.Fatalf("scaled task gap=%d", gap)
	}
}

func TestListNarrowAndUnsupportedTaskNotation(t *testing.T) {
	for _, width := range []int{1, 7, 48, 120} {
		doc := listDocument(t, "9999. A\n10000. B\n\n- [ ] C\n  - D\n", width, 1)
		for _, text := range []string{"A", "B", "C", "D"} {
			listGlyph(t, doc, text)
		}
		doc.paint(0, min(doc.height, 200))
	}
	doc := listDocument(t, "- [~] WIP\n", 600, 1)
	if len(listCheckboxes(doc)) != 0 || !strings.Contains(sceneText(doc), "[~] WIP") {
		t.Fatal("unsupported task notation changed")
	}
}
