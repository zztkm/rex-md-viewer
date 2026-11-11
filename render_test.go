package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func testFonts(t *testing.T) *fontSet {
	t.Helper()
	f, err := loadFonts("", "")
	if err != nil {
		t.Skipf("Japanese font unavailable: %v", err)
	}
	t.Cleanup(f.close)
	if _, ok := f.sourceFor('日', false, false); !ok {
		t.Skip("Japanese font unavailable")
	}
	return f
}
func layoutText(t *testing.T, source string, width int) *document {
	t.Helper()
	doc, err := layoutMarkdown([]byte(source), filepath.Join("testdata", "test.md"), width, 1, testFonts(t), paletteFor("light"))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func sceneText(doc *document) string {
	var b strings.Builder
	for _, op := range doc.ops {
		b.WriteString(op.text)
	}
	return b.String()
}

func TestMarkdownRendering(t *testing.T) {
	doc := layoutText(t, "# 日本語の見出し\n\nplain **bold** *em* ~~gone~~ [link](https://example.com) &amp; \\*escaped\\*\n\n- [ ] pending\n- [x] done\n\n> quote\n\n3. third\n4. fourth\n\n---\n\n<script>alert('safe')</script>\n\n<https://example.com>", 500)
	text := sceneText(doc)
	for _, want := range []string{"日本語の見出し", "bold", "em", "gone", "link", "&", "*escaped*", "pending", "done", "quote", "3.", "4.", "<script>", "https://example.com"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	hasBold, hasUnderline, hasStrike := false, false, false
	for _, op := range doc.ops {
		hasBold = hasBold || op.style.bold
		hasUnderline = hasUnderline || op.style.underline
		hasStrike = hasStrike || op.style.strike
	}
	if !hasBold || !hasUnderline || !hasStrike {
		t.Fatal("inline styles lost")
	}
	if doc.height <= 100 {
		t.Fatalf("height=%d", doc.height)
	}
	img := doc.paint(0, doc.height)
	if img.Bounds().Dx() != 500 {
		t.Fatal("wrong width")
	}
}
func TestCodeSpanAndHTMLAreLiteral(t *testing.T) {
	doc := layoutText(t, "`&amp; \\*` &amp;\n\n<script>\nalert('safe');\n</script>\n", 500)
	got := sceneText(doc)
	for _, want := range []string{"&amp; \\*", " &", "<script>", "alert('safe');", "</script>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestLineEndings(t *testing.T) {
	lf := "# 日本語\n\n~~~\n  code\n~~~\n"
	want := sceneText(layoutText(t, lf, 300))
	for _, newline := range []string{"\r\n", "\r"} {
		got := sceneText(layoutText(t, strings.ReplaceAll(lf, "\n", newline), 300))
		if got != want {
			t.Fatalf("line ending %q: %q != %q", newline, got, want)
		}
	}
}

func TestCodeAndWrapping(t *testing.T) {
	doc := layoutText(t, "日本語とEnglishMixedWordsAreWrappedCorrectly\n\n~~~go\n    a  b\n\t日本語\n~~~\n", 100)
	if !strings.Contains(sceneText(doc), "    a  b") {
		t.Fatal("code whitespace lost")
	}
	rows := map[int]bool{}
	for _, op := range doc.ops {
		if op.text != "" {
			rows[op.rect.Min.Y] = true
			if op.rect.Max.X > doc.width {
				t.Fatalf("overflow: %+v", op)
			}
		}
	}
	if len(rows) < 5 {
		t.Fatal("text did not wrap")
	}
	l := layout{doc: doc, size: 16}
	st := textStyle{size: 16, code: true}
	if l.charWidth('i', st) != l.charWidth('W', st) {
		t.Fatal("code is not monospaced")
	}
}
func TestViewportMatchesFullDocument(t *testing.T) {
	doc := layoutText(t, strings.Repeat("## 見出し\n\n段落 **太字** と本文。\n\n", 20), 400)
	full := doc.paint(0, doc.height)
	view := doc.paint(113, 200)
	crop := image.NewRGBA(view.Bounds())
	draw.Draw(crop, crop.Bounds(), full, image.Pt(0, 113), draw.Src)
	if !bytes.Equal(view.Pix, crop.Pix) {
		t.Fatal("viewport differs from full-document crop")
	}
}
func TestNarrowTableAndEmptyDocument(t *testing.T) {
	for _, width := range []int{1, 7, 48, 120} {
		doc := layoutText(t, "| left | center | right |\n| :--- | :---: | ---: |\n| 日本語 | bold | 123 |\n", width)
		if !strings.Contains(sceneText(doc), "日本語") {
			t.Fatalf("table contents lost at width %d", width)
		}
		doc.paint(0, min(doc.height, 200))
	}
	if text := sceneText(layoutText(t, "", 200)); text != "Empty file" {
		t.Fatal(text)
	}
}
func TestLocalImagesAndNoNetwork(t *testing.T) {
	fonts := testFonts(t)
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{200, 50, 20, 255}), image.Point{}, draw.Src)
	path := filepath.Join(dir, "image space.png")
	f, _ := os.Create(path)
	png.Encode(f, img)
	f.Close()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	source := "![local](image%20space.png)\n\n![remote](" + server.URL + "/img.png)\n\n![missing](missing.png)"
	doc, err := layoutMarkdown([]byte(source), filepath.Join(dir, "doc.md"), 80, 1, fonts, paletteFor("dark"))
	if err != nil {
		t.Fatal(err)
	}
	pictures := 0
	for _, op := range doc.ops {
		if op.picture != nil {
			pictures++
			if op.picture.Bounds().Dx() > 80 {
				t.Fatal("image not scaled")
			}
		}
	}
	if pictures != 1 || calls.Load() != 0 {
		t.Fatalf("pictures=%d network=%d", pictures, calls.Load())
	}
	if !strings.Contains(sceneText(doc), "remote") || !strings.Contains(sceneText(doc), "missing") {
		t.Fatal("image fallback missing")
	}
}
func TestGIFFirstFrame(t *testing.T) {
	dir := t.TempDir()
	pal := color.Palette{color.Black, color.White}
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), pal)
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), pal)
	for i := range second.Pix {
		second.Pix[i] = 1
	}
	f, _ := os.Create(filepath.Join(dir, "animation.gif"))
	gif.EncodeAll(f, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{1, 1}})
	f.Close()
	l := layout{baseDir: dir, images: make(map[string]image.Image)}
	got, err := l.localImage("animation.gif", 20)
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := got.At(0, 0).RGBA(); r != 0 {
		t.Fatal("not first frame")
	}
}
func TestRenderProtectsInputAndAliases(t *testing.T) {
	fonts := testFonts(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "doc.md")
	os.WriteFile(input, []byte("# safe"), 0600)
	aliases := []string{input, filepath.Join(dir, "symlink"), filepath.Join(dir, "hardlink")}
	os.Symlink(input, aliases[1])
	os.Link(input, aliases[2])
	for _, output := range aliases {
		if err := renderFile(options{file: input, output: output, width: 100, theme: "light"}, []byte("# safe"), fonts); err == nil {
			t.Fatalf("overwrote %s", output)
		}
	}
	output := filepath.Join(dir, "out.png")
	if err := renderFile(options{file: input, output: output, width: 100, theme: "light"}, []byte("# safe"), fonts); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if cfg, err := png.DecodeConfig(f); err != nil || cfg.Width != 100 {
		t.Fatalf("%+v %v", cfg, err)
	}
}
func TestRenderPixelLimit(t *testing.T) {
	fonts := testFonts(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "long.md")
	data := []byte(strings.Repeat("line\n\n", 5000))
	os.WriteFile(input, data, 0600)
	err := renderFile(options{file: input, output: filepath.Join(dir, "out.png"), width: 1000, theme: "light"}, data, fonts)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("%v", err)
	}
}
func TestInvalidFontsAndSafeText(t *testing.T) {
	if _, err := loadFonts("/nonexistent/font.ttf", ""); err == nil {
		t.Fatal("missing font accepted")
	}
	got := safeText("日本語\x1b[31m\x00\n")
	if strings.ContainsAny(got, "\x1b\x00") || !strings.Contains(got, "日本語") {
		t.Fatal(got)
	}
}
func BenchmarkLayout100KiB(b *testing.B) {
	f, err := loadFonts("", "")
	if err != nil {
		b.Skip(err)
	}
	defer f.close()
	if _, ok := f.sourceFor('日', false, false); !ok {
		b.Skip("Japanese font unavailable")
	}
	data := []byte(strings.Repeat("日本語を含む段落と **太字** English text.\n\n", 1800))
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := layoutMarkdown(data, "bench.md", 900, 1.5, f, paletteFor("dark")); err != nil {
			b.Fatal(err)
		}
	}
}
