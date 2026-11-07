package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

func fontFixture(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestPreferredFontOrderAndFallback(t *testing.T) {
	dir := t.TempDir()
	explicit := fontFixture(t, dir, "explicit.ttf", goregular.TTF)
	preference := fontFixture(t, dir, "environment.ttf", gomono.TTF)
	candidate := fontFixture(t, dir, "installed.ttf", gobold.TTF)
	broken := fontFixture(t, dir, "broken.ttf", []byte("not a font"))
	missing := filepath.Join(dir, "missing.ttf")
	cases := []struct {
		name, explicit, preference string
		candidates                 []string
		want                       string
		wantErr                    bool
	}{
		{"cli", explicit, preference, []string{candidate}, explicit, false},
		{"environment", "", preference, []string{candidate}, preference, false},
		{"candidate", "", missing, []string{missing, broken, candidate}, candidate, false},
		{"bad-environment", "", broken, []string{candidate}, candidate, false},
		{"embedded", "", "", []string{missing, broken}, "", false},
		{"bad-cli", missing, preference, []string{candidate}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, path, err := preferredFont(tc.explicit, tc.preference, tc.candidates, goregular.TTF)
			if (err != nil) != tc.wantErr {
				t.Fatalf("%v", err)
			}
			if err == nil && (path != tc.want || !hasGlyph(f, 'A')) {
				t.Fatalf("path=%q, want=%q", path, tc.want)
			}
		})
	}
}
func TestFontEnvironmentAndCLIOverride(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	body := fontFixture(t, dir, "environment body.ttf", goregular.TTF)
	bold := fontFixture(t, dir, "environment bold.ttf", gobold.TTF)
	explicit := fontFixture(t, dir, "explicit.ttf", gomono.TTF)
	t.Setenv(fontEnv, filepath.Base(body))
	t.Setenv(boldFontEnv, filepath.Base(bold))
	f, err := loadFonts("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer f.close()
	if f.bodyPath != body || f.boldPath != bold {
		t.Fatalf("paths=%q %q", f.bodyPath, f.boldPath)
	}
	// A Latin-only font is valid; Japanese is handled separately per glyph.
	if hasJapanese(f.body) {
		t.Fatal("test fixture unexpectedly includes Japanese")
	}
	override, err := loadFonts(explicit, "")
	if err != nil {
		t.Fatal(err)
	}
	defer override.close()
	if override.bodyPath != explicit || override.boldPath != bold {
		t.Fatal("CLI did not override only the regular font")
	}
}
func TestJapaneseGlyphFallback(t *testing.T) {
	dir := t.TempDir()
	regular := fontFixture(t, dir, "body.ttf", goregular.TTF)
	bold := fontFixture(t, dir, "bold.ttf", gobold.TTF)
	f, err := loadFonts(regular, bold)
	if err != nil {
		t.Fatal(err)
	}
	defer f.close()
	if f.japanese == nil {
		t.Skip("Japanese system font unavailable")
	}
	if source, ok := f.sourceFor('A', false, false); !ok || source != f.body {
		t.Fatal("Latin primary font ignored")
	}
	if source, ok := f.sourceFor('日', false, false); !ok || source != f.japanese {
		t.Fatal("Japanese regular fallback missing")
	}
	if source, ok := f.sourceFor('日', true, false); !ok || source != f.japaneseBold {
		t.Fatal("Japanese bold fallback missing")
	}
	face := f.runeFace(16, false, false, '日')
	if face != f.runeFace(16, false, false, '日') {
		t.Fatal("face cache missed")
	}
	if source, _ := f.sourceFor('A', false, true); source != f.mono {
		t.Fatal("code ASCII font changed")
	}
}
func TestEmbeddedFontsWithoutJapanese(t *testing.T) {
	body, _ := opentype.Parse(goregular.TTF)
	bold, _ := opentype.Parse(gobold.TTF)
	mono, _ := opentype.Parse(gomono.TTF)
	f := &fontSet{body: body, bold: bold, mono: mono, faces: make(map[faceKey]font.Face)}
	defer f.close()
	doc, err := layoutMarkdown([]byte("# English only"), "doc.md", 400, 1, f, paletteFor("light"))
	if err != nil {
		t.Fatal(err)
	}
	doc.paint(0, doc.height)
	_, err = layoutMarkdown([]byte("日本語"), "doc.md", 400, 1, f, paletteFor("light"))
	if err == nil || !strings.Contains(err.Error(), fontEnv) {
		t.Fatalf("missing Japanese font error: %v", err)
	}
}
func TestRunForwardsResolvedEnvironmentFonts(t *testing.T) {
	statePath := setupFakeRex(t)
	dir := t.TempDir()
	body := fontFixture(t, dir, "font body.ttf", goregular.TTF)
	bold := fontFixture(t, dir, "font bold.ttf", gobold.TTF)
	t.Setenv(fontEnv, body)
	t.Setenv(boldFontEnv, bold)
	path := filepath.Join(dir, "doc.md")
	os.WriteFile(path, []byte("# English"), 0600)
	if err := run([]string{"view", path}, io.Discard); err != nil {
		t.Fatal(err)
	}
	state := readFakeState(t, statePath)
	for _, want := range []string{fontEnv + "=" + body, boldFontEnv + "=" + bold} {
		found := false
		for _, arg := range state.SplitArgs {
			found = found || arg == want
		}
		if !found {
			t.Fatalf("missing resolved environment preference: %q", want)
		}
	}
	for _, pair := range []struct{ flag, value string }{{"--font", body}, {"--font-bold", bold}} {
		found := false
		for i, arg := range state.SplitArgs {
			if arg == pair.flag && i+1 < len(state.SplitArgs) && state.SplitArgs[i+1] == pair.value {
				found = true
			}
		}
		if !found {
			t.Fatalf("child does not pin %s", pair.flag)
		}
	}
}
