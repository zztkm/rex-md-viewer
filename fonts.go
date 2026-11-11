package main

import (
	"fmt"
	"os"
	"path/filepath"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

const fontEnv = "REX_MD_VIEWER_FONT"
const boldFontEnv = "REX_MD_VIEWER_FONT_BOLD"

type faceKey struct {
	size   int
	source *sfnt.Font
}
type fontSet struct {
	body, bold, mono       *sfnt.Font
	japanese, japaneseBold *sfnt.Font
	bodyPath, boldPath     string
	faces                  map[faceKey]font.Face
}

type fontPaths struct{ body, bold, japanese, japaneseBold []string }

// The primary font order matches rexpad: static SF Pro Text, Inter, then
// embedded Go fonts. Japanese fonts supplement missing glyphs, not this order.
func defaultFontPaths(home string) fontPaths {
	return fontPaths{
		body: []string{
			"/Library/Fonts/SF-Pro-Text-Regular.otf",
			filepath.Join(home, "Library/Fonts/SF-Pro-Text-Regular.otf"),
			"/usr/share/fonts/TTF/Inter-Regular.ttf",
			"/usr/share/fonts/truetype/inter/Inter-Regular.ttf",
			"/usr/share/fonts/inter/Inter-Regular.otf",
		},
		bold: []string{
			"/Library/Fonts/SF-Pro-Text-Bold.otf",
			filepath.Join(home, "Library/Fonts/SF-Pro-Text-Bold.otf"),
			"/usr/share/fonts/TTF/Inter-Bold.ttf",
			"/usr/share/fonts/truetype/inter/Inter-Bold.ttf",
			"/usr/share/fonts/inter/Inter-Bold.otf",
		},
		japanese: []string{
			filepath.Join(home, "Library/Fonts/NotoSansJP-Regular.ttf"),
			"/System/Library/Fonts/ヒラギノ角ゴシック W3.ttc",
			"/System/Library/Fonts/ヒラギノ丸ゴ ProN W4.ttc",
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/noto/NotoSansJP-Regular.ttf",
		},
		japaneseBold: []string{
			filepath.Join(home, "Library/Fonts/NotoSansJP-Bold.ttf"),
			"/System/Library/Fonts/ヒラギノ角ゴシック W6.ttc",
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc",
			"/usr/share/fonts/truetype/noto/NotoSansJP-Bold.ttf",
		},
	}
}

func parseFont(path string) (*sfnt.Font, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	collection, err := sfnt.ParseCollection(b)
	if err != nil {
		return nil, err
	}
	var first *sfnt.Font
	for i := 0; i < collection.NumFonts(); i++ {
		f, e := collection.Font(i)
		if e != nil {
			continue
		}
		if first == nil {
			first = f
		}
		if hasJapanese(f) {
			return f, nil
		}
	}
	if first == nil {
		return nil, fmt.Errorf("cannot load font: %s", path)
	}
	return first, nil
}
func hasGlyph(f *sfnt.Font, r rune) bool {
	if f == nil {
		return false
	}
	idx, err := f.GlyphIndex(nil, r)
	return err == nil && idx != 0
}
func hasJapanese(f *sfnt.Font) bool {
	for _, r := range "日本語あ" {
		if !hasGlyph(f, r) {
			return false
		}
	}
	return true
}
func isJapanese(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}

// Explicit CLI errors remain errors. Like rexpad, an unusable environment
// preference or installed candidate falls through to the next available font.
func preferredFont(explicit, preference string, candidates []string, bundled []byte) (*sfnt.Font, string, error) {
	if explicit != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return nil, "", err
		}
		f, err := parseFont(path)
		return f, path, err
	}
	if preference != "" {
		candidates = append([]string{preference}, candidates...)
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if f, err := parseFont(path); err == nil {
			return f, path, nil
		}
	}
	f, err := opentype.Parse(bundled)
	return f, "", err
}
func japaneseFont(candidates []string) *sfnt.Font {
	for _, path := range candidates {
		if f, err := parseFont(path); err == nil && hasJapanese(f) {
			return f
		}
	}
	return nil
}
func loadFonts(bodyPath, boldPath string) (*fontSet, error) {
	home, _ := os.UserHomeDir()
	paths := defaultFontPaths(home)
	body, bodyPath, err := preferredFont(bodyPath, os.Getenv(fontEnv), paths.body, goregular.TTF)
	if err != nil {
		return nil, fmt.Errorf("--font: %w", err)
	}
	bold, boldPath, err := preferredFont(boldPath, os.Getenv(boldFontEnv), paths.bold, gobold.TTF)
	if err != nil {
		return nil, fmt.Errorf("--font-bold: %w", err)
	}
	mono, err := opentype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	jp := japaneseFont(paths.japanese)
	jpBold := japaneseFont(paths.japaneseBold)
	if jpBold == nil {
		jpBold = jp
	}
	return &fontSet{body: body, bold: bold, mono: mono, japanese: jp, japaneseBold: jpBold, bodyPath: bodyPath, boldPath: boldPath, faces: make(map[faceKey]font.Face)}, nil
}

func (f *fontSet) sourceFor(r rune, bold, mono bool) (*sfnt.Font, bool) {
	if mono && hasGlyph(f.mono, r) {
		return f.mono, true
	}
	var candidates []*sfnt.Font
	if bold {
		candidates = []*sfnt.Font{f.bold, f.body, f.japaneseBold, f.japanese}
	} else {
		candidates = []*sfnt.Font{f.body, f.japanese}
	}
	for _, src := range candidates {
		if hasGlyph(src, r) {
			return src, true
		}
	}
	// Non-Japanese missing symbols retain the selected font's .notdef glyph.
	// Required Japanese text is checked by the layout before painting.
	return f.body, false
}
func (f *fontSet) face(size int, src *sfnt.Font) font.Face {
	k := faceKey{size: size, source: src}
	if face := f.faces[k]; face != nil {
		return face
	}
	face, _ := opentype.NewFace(src, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingNone})
	f.faces[k] = face
	return face
}
func (f *fontSet) runeFace(size int, bold, mono bool, r rune) font.Face {
	src, _ := f.sourceFor(r, bold, mono)
	return f.face(size, src)
}
func (f *fontSet) baseline(size int) int {
	ascent := size
	for _, src := range []*sfnt.Font{f.body, f.bold, f.mono, f.japanese, f.japaneseBold} {
		if src != nil {
			ascent = max(ascent, f.face(size, src).Metrics().Ascent.Ceil())
		}
	}
	return ascent
}
func (f *fontSet) close() {
	for _, face := range f.faces {
		face.Close()
	}
}
