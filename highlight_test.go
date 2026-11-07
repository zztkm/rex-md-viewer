package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func codeGlyphs(source string, colors palette) []paintOp {
	var ops []paintOp
	for _, r := range strings.ReplaceAll(safeText(source), "\t", "    ") {
		if r != '\n' {
			ops = append(ops, paintOp{text: string(r), style: textStyle{code: true, ink: colors.fg}})
		}
	}
	return ops
}

func TestCodeHighlightLanguages(t *testing.T) {
	for _, tc := range []struct{ language, source string }{
		{"go", "package main\nfunc main() { println(42) }"},
		{"Go", "package main\nfunc main() { println(42) }"},
		{"python", "def greet():\n\treturn 'hello'"},
		{"py", "def greet():\n\treturn 'hello'"},
		{"bash", "#!/bin/bash\necho \"hello $USER\""},
		{"sh", "echo \"hello $USER\""},
		{"javascript", "const message = \"hello\";"},
		{"js", "const message = \"hello\";"},
		{"json", "{\"answer\": 42, \"ok\": true}"},
		{"yaml", "answer: 42\nmessage: \"hello\""},
	} {
		t.Run(tc.language, func(t *testing.T) {
			for _, theme := range []string{"light", "dark"} {
				colors := paletteFor(theme)
				ops := codeGlyphs(tc.source, colors)
				highlightCode(ops, tc.source, tc.language, colors)
				highlighted := false
				for _, op := range ops {
					highlighted = highlighted || op.style.ink != colors.fg
				}
				if !highlighted {
					t.Errorf("%s: no syntax colours", theme)
				}
			}
		})
	}
}

func TestCodeHighlightTokenColors(t *testing.T) {
	source := "// コメント\nfunc main() {\n\tprintln(\"日本語\", 42)\n}"
	var keywords []color.RGBA
	for _, theme := range []string{"light", "dark"} {
		colors := paletteFor(theme)
		ops := codeGlyphs(source, colors)
		highlightCode(ops, source, "go", colors)
		text := sceneText(&document{ops: ops})
		seen := map[color.RGBA]bool{}
		for _, token := range []string{"// コメント", "func", "\"日本語\"", "42"} {
			offset := len([]rune(text[:strings.Index(text, token)]))
			ink := ops[offset].style.ink
			if ink == colors.fg || ink.A != 255 {
				t.Errorf("%s: token %q has no syntax colour: %v", theme, token, ink)
			}
			for i := range len([]rune(token)) {
				if ops[offset+i].style.ink != ink {
					t.Errorf("%s: inconsistent colour inside %q", theme, token)
				}
			}
			seen[ink] = true
			if token == "func" {
				keywords = append(keywords, ink)
			}
		}
		if len(seen) < 3 {
			t.Errorf("%s: token kinds are not distinguished: %v", theme, seen)
		}
	}
	if keywords[0] == keywords[1] {
		t.Fatal("syntax colours did not change with the theme")
	}
}

func TestCodeHighlightMultilineAndControlText(t *testing.T) {
	source := "/* 日本語\n\tcomment */\nvar message = `first\n\t日本語\x00\x1b &amp;`\nconst answer = 42"
	for _, theme := range []string{"light", "dark"} {
		colors := paletteFor(theme)
		ops := codeGlyphs(source, colors)
		highlightCode(ops, source, "go", colors)
		text := sceneText(&document{ops: ops})
		for _, token := range []string{"/* 日本語    comment */", "`first    日本語\uFFFD\uFFFD &amp;`", "const", "42"} {
			offset := len([]rune(text[:strings.Index(text, token)]))
			ink := ops[offset].style.ink
			if ink == colors.fg {
				t.Fatalf("%s: multiline/control text offset lost at %q", theme, token)
			}
			for i := range len([]rune(token)) {
				if ops[offset+i].style.ink != ink {
					t.Fatalf("%s: multiline token colour shifted in %q", theme, token)
				}
			}
		}
	}
}

func TestCodeHighlightErrorTokensRemainReadable(t *testing.T) {
	name := "rex-md-test-error-token"
	lexers.Register(chroma.MustNewLexer(&chroma.Config{Name: name}, func() chroma.Rules {
		return chroma.Rules{"root": {{Pattern: ".+", Type: chroma.Error}}}
	}))
	for _, theme := range []string{"light", "dark"} {
		colors := paletteFor(theme)
		ops := codeGlyphs("invalid code", colors)
		highlightCode(ops, "invalid code", name, colors)
		for _, op := range ops {
			if op.style.ink != colors.fg {
				t.Fatalf("%s: error token depends on an undrawn background", theme)
			}
		}
	}
}

func TestCodeHighlightTerminalPalette(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		standard := paletteFor(theme)
		terminal := makePalette(standard.bg, color.RGBA{150, 110, 100, 255})
		source := "func main() {}"
		want, got := codeGlyphs(source, standard), codeGlyphs(source, terminal)
		highlightCode(want, source, "go", standard)
		highlightCode(got, source, "go", terminal)
		if got[0].style.ink != want[0].style.ink {
			t.Errorf("%s: terminal background did not select matching syntax colours", theme)
		}
		if got[4].style.ink != terminal.fg {
			t.Errorf("%s: ordinary whitespace lost the terminal foreground", theme)
		}
	}
}

func TestCodeHighlightFallback(t *testing.T) {
	colors := paletteFor("light")
	for _, tc := range []struct{ name, language, source string }{
		{"unspecified", "", "func main() {}"},
		{"unknown", "not-a-known-language", "func main() {}"},
		{"plain", "text", "func main() {}"},
		{"empty", "go", ""},
		{"large", "go", strings.Repeat("x", maxHighlightBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := codeGlyphs(tc.source, colors)
			highlightCode(ops, tc.source, tc.language, colors)
			for _, op := range ops {
				if op.style.ink != colors.fg {
					t.Fatal("fallback was not plain text")
				}
			}
		})
	}
}

func TestCodeHighlightLexerFailure(t *testing.T) {
	// The iterator can fail after it has already coloured some glyphs.
	for _, failure := range []string{"panic", "altered-input"} {
		name := "rex-md-test-" + failure
		lexers.Register(chroma.MustNewLexer(&chroma.Config{Name: name}, func() chroma.Rules {
			return chroma.Rules{"root": {
				{Pattern: "func", Type: chroma.Keyword},
				{Pattern: ".+", Type: chroma.EmitterFunc(func(_ []string, _ *chroma.LexerState) chroma.Iterator {
					return func() chroma.Token {
						if failure == "panic" {
							panic("lexer failed")
						}
						return chroma.Token{Type: chroma.Keyword, Value: "not the original input"}
					}
				})},
			}}
		}))
		colors := paletteFor("dark")
		ops := codeGlyphs("func main() {}", colors)
		highlightCode(ops, "func main() {}", name, colors)
		for _, op := range ops {
			if op.style.ink != colors.fg {
				t.Fatalf("%s: partially highlighted block after failure", failure)
			}
		}
	}
}

func TestHighlightedCodeLayoutIsUnchanged(t *testing.T) {
	fonts := testFonts(t)
	source := "\n// 日本語 <tag> &amp;\nfunc main() {\n\tprintln(\"a long string with spaces\", 42)\n    x := a+b*c\n\t\x1b[31m\n}\n\n"
	for _, width := range []int{1, 48, 100, 200, 600} {
		for _, theme := range []string{"light", "dark"} {
			colors := paletteFor(theme)
			layoutCode := func(language string) *document {
				t.Helper()
				doc, err := layoutMarkdown([]byte("~~~"+language+" extra-info\n"+source+"~~~\n"), "code.md", width, 1, fonts, colors)
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}
			plain, highlighted := layoutCode("unknown-language"), layoutCode("go")
			if plain.height != highlighted.height || len(plain.ops) != len(highlighted.ops) {
				t.Fatalf("%s width=%d: layout dimensions changed", theme, width)
			}
			hasHighlight := false
			for i, want := range plain.ops {
				got := highlighted.ops[i]
				hasHighlight = hasHighlight || (got.text != "" && got.style.ink != colors.fg)
				if got.rect != want.rect || got.text != want.text {
					t.Fatalf("%s width=%d op=%d: code text or position changed", theme, width, i)
				}
				got.style.ink = want.style.ink
				if got.style != want.style {
					t.Fatalf("%s width=%d: non-colour style changed", theme, width)
				}
			}
			if !hasHighlight {
				t.Fatalf("%s width=%d: fenced code was not highlighted", theme, width)
			}
			wantText := strings.ReplaceAll(strings.ReplaceAll(safeText(source), "\n", ""), "\t", "    ")
			if sceneText(highlighted) != wantText {
				t.Fatalf("%s width=%d: literal code text lost", theme, width)
			}
			full := highlighted.paint(0, highlighted.height)
			view := highlighted.paint(37, 100)
			crop := image.NewRGBA(view.Bounds())
			draw.Draw(crop, crop.Bounds(), full, image.Pt(0, 37), draw.Src)
			if !bytes.Equal(view.Pix, crop.Pix) {
				t.Fatalf("%s width=%d: viewport differs from full image", theme, width)
			}
			if !bytes.Equal(full.Pix, highlighted.paint(0, highlighted.height).Pix) {
				t.Fatal("code rendering is not deterministic")
			}
		}
	}
}

func TestUnspecifiedCodeRemainsPlain(t *testing.T) {
	for _, source := range []string{
		"~~~\nfunc main() {}\n~~~\n",
		"    func main() {}\n",
		"`func main() {}`\n",
		"<script>const value = 42;</script>\n",
	} {
		doc := layoutText(t, source, 500)
		for _, op := range doc.ops {
			if op.text != "" && op.style.ink != doc.colors.fg {
				t.Fatalf("unexpected highlighting in %q", source)
			}
		}
	}
}

func BenchmarkHighlight100KiB(b *testing.B) {
	source := strings.Repeat("func main() { println(\"hello\", 42) }\n", 2900)
	colors := paletteFor("dark")
	ops := codeGlyphs(source, colors)
	b.SetBytes(int64(len(source)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		highlightCode(ops, source, "go", colors)
	}
}
