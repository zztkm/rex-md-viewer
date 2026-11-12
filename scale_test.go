package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestScaleOptions(t *testing.T) {
	for _, command := range []string{"view", "render"} {
		for _, tc := range []struct {
			name, env string
			flags     []string
			want      float64
			badSource string
		}{
			{name: "default", want: 1},
			{name: "environment", env: "1.2", want: 1.2},
			{name: "minimum", flags: []string{"--scale", "0.5"}, want: 0.5},
			{name: "maximum", flags: []string{"--scale=4"}, want: 4},
			{name: "cli overrides environment", env: "2", flags: []string{"--scale", "1.5"}, want: 1.5},
			{name: "explicit default overrides environment", env: "2", flags: []string{"--scale", "1"}, want: 1},
			{name: "cli overrides invalid environment", env: "invalid", flags: []string{"--scale", "1"}, want: 1},
			{name: "empty cli is invalid", env: "2", flags: []string{"--scale="}, badSource: "--scale"},
			{name: "invalid environment", env: "invalid", badSource: "REX_MD_VIEWER_SCALE"},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				t.Setenv("REX_MD_VIEWER_SCALE", tc.env)
				args := append([]string(nil), tc.flags...)
				if command == "render" {
					args = append(args, "-o", "out.png")
				}
				args = append(args, "doc.md")
				o, err := parseOptions(command, args, io.Discard)
				if tc.badSource != "" {
					assertScaleUsageError(t, err, tc.badSource)
				} else if err != nil || o.scale != tc.want {
					t.Fatalf("scale=%g err=%v, want %g", o.scale, err, tc.want)
				}
			})
		}
	}
	for _, value := range []string{"0", "-1", "0.499999", "4.000001", "NaN", "Inf", "-Inf", "1e999", "bad", " "} {
		for _, source := range []string{"--scale", "REX_MD_VIEWER_SCALE"} {
			t.Run(source+"/"+value, func(t *testing.T) {
				t.Setenv("REX_MD_VIEWER_SCALE", "")
				args := []string{"doc.md"}
				if source == "--scale" {
					args = append([]string{"--scale", value}, args...)
				} else {
					t.Setenv(source, value)
				}
				_, err := parseOptions("view", args, io.Discard)
				assertScaleUsageError(t, err, source)
			})
		}
	}
	for _, flags := range [][]string{{"--in-place"}, {"--below", "--ratio", "40"}} {
		args := append(append([]string(nil), flags...), "--scale", "1.2", "doc.md")
		if _, err := parseOptions("view", args, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
}

func assertScaleUsageError(t *testing.T, err error, source string) {
	t.Helper()
	var usage *usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), source) || !strings.Contains(err.Error(), "0.5") || !strings.Contains(err.Error(), "4") {
		t.Fatalf("want usage error naming %s and range, got %v", source, err)
	}
}

func TestInvalidScaleHasNoSideEffects(t *testing.T) {
	state := setupFakeRex(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "missing.md")
	output := filepath.Join(dir, "keep.png")
	if err := os.WriteFile(output, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"view", "--scale", "NaN", file},
		{"view", "--in-place", "--scale", "0", file},
		{"render", "--scale", "5", "-o", output, file},
	} {
		err := run(args, io.Discard)
		assertScaleUsageError(t, err, "--scale")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("invalid scale contacted Rex: %v", err)
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "keep" {
		t.Fatalf("output changed: %q %v", got, err)
	}
	t.Setenv("REX_MD_VIEWER_SCALE", "invalid")
	if err := run([]string{"hide", file}, io.Discard); err != nil {
		t.Fatalf("hide reads rendering preferences: %v", err)
	}
	for _, args := range [][]string{{"--help"}, {"view", "--help"}, {"render", "--help"}, {"--version"}, {"skill", "show"}} {
		if err := run(args, io.Discard); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestScaleChildRoundTrip(t *testing.T) {
	for _, scale := range []string{"0.5", "1", "1.2345678901234567", "4"} {
		t.Run(scale, func(t *testing.T) {
			t.Setenv("REX_MD_VIEWER_SCALE", scale)
			o, err := parseOptions("view", []string{"--", "/tmp/日本語 '$ ;.md"}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("REX_MD_VIEWER_SCALE", "invalid in server environment")
			child, err := parseOptions("view", childArgs("viewer", o)[2:], io.Discard)
			if err != nil || child.scale != o.scale || child.file != o.file || !child.inPlace {
				t.Fatalf("child=%+v err=%v parent=%+v", child, err, o)
			}
		})
	}
}

func TestPaneScaleReuse(t *testing.T) {
	state := setupFakeRex(t)
	file := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(file, []byte("scale"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_MD_VIEWER_SCALE", "1.2")
	if err := run([]string{"view", file}, io.Discard); err != nil {
		t.Fatal(err)
	}
	first := readFakeState(t, state)
	index := 0
	for index < len(first.SplitArgs) && first.SplitArgs[index] != "view" {
		index++
	}
	if index == len(first.SplitArgs) {
		t.Fatal("missing child viewer")
	}
	t.Setenv("REX_MD_VIEWER_SCALE", "bad")
	child, err := parseOptions("view", first.SplitArgs[index+1:], io.Discard)
	if err != nil || child.scale != 1.2 {
		t.Fatalf("scale not forwarded: %+v %v", child, err)
	}
	if err := run([]string{"view", "--scale", "2", file}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := readFakeState(t, state); got.Splits != 1 || got.Closes != 0 || !reflect.DeepEqual(got.SplitArgs, first.SplitArgs) {
		t.Fatalf("scale changed existing pane: %+v", got)
	}
}

func TestRenderScale(t *testing.T) {
	t.Setenv("REX_MD_VIEWER_SCALE", "")
	dir := t.TempDir()
	file := filepath.Join(dir, "doc.md")
	source := []byte(strings.Repeat("one two three four five six seven eight nine ten ", 5))
	if err := os.WriteFile(file, source, 0600); err != nil {
		t.Fatal(err)
	}
	fonts := testFonts(t)
	baseline, err := layoutMarkdown(source, file, 300, 1, fonts, paletteFor("light"))
	if err != nil {
		t.Fatal(err)
	}
	var previousHeight int
	for _, scale := range []string{"0.5", "1", "1.5", "4"} {
		output := filepath.Join(dir, scale+".png")
		if err := run([]string{"render", "--width", "300", "--scale", scale, "-o", output, file}, io.Discard); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 300 || img.Bounds().Dy() <= previousHeight {
			t.Fatalf("scale=%s bounds=%v previous height=%d", scale, img.Bounds(), previousHeight)
		}
		if scale == "1" && !reflect.DeepEqual(img, baseline.paint(0, baseline.height)) {
			t.Fatal("scale=1 changed the existing rendering")
		}
		previousHeight = img.Bounds().Dy()
	}
}

func TestScaleLayout(t *testing.T) {
	fonts := testFonts(t)
	dir := t.TempDir()
	picture := image.NewRGBA(image.Rect(0, 0, 30, 20))
	picture.Set(0, 0, color.RGBA{255, 0, 0, 255})
	if err := writePNG(filepath.Join(dir, "picture.png"), picture); err != nil {
		t.Fatal(err)
	}
	source := []byte("# Heading\n\n" + strings.Repeat("word ", 100) + "\n\n- [~] [active](https://example.com)\n\n![image](picture.png)\n")
	var previousSize, previousHeight, previousRows, previousBox int
	for _, scale := range []float64{0.5, 1, 1.2, 2, 4} {
		doc, err := layoutMarkdown(source, filepath.Join(dir, "doc.md"), 500, scale, fonts, paletteFor("light"))
		if err != nil {
			t.Fatal(err)
		}
		size, box := 0, 0
		hasPicture := false
		rows := map[int]bool{}
		for _, op := range doc.ops {
			if op.text == "w" {
				size = op.style.size
				rows[op.rect.Min.Y] = true
			}
			if op.checkbox != nil {
				box = op.rect.Dx()
			}
			if op.picture != nil {
				hasPicture = true
				if op.picture.Bounds() != picture.Bounds() {
					t.Fatalf("scale=%g changed small image bounds: %v", scale, op.picture.Bounds())
				}
			}
		}
		if size <= previousSize || doc.height <= previousHeight || len(rows) <= previousRows || box <= previousBox {
			t.Fatalf("scale=%g size=%d height=%d rows=%d box=%d", scale, size, doc.height, len(rows), box)
		}
		if len(doc.dots) == 0 || len(doc.links) == 0 || !hasPicture {
			t.Fatal("scale lost active dots, links, or image")
		}
		for _, region := range doc.links {
			point := region.rect.Min.Add(image.Pt(region.rect.Dx()/2, region.rect.Dy()/2))
			size := terminalSize{cols: 50, rows: 30, width: 500, height: 600}
			state := linkInput{pixels: true, keyboard: true, mods: 8}
			offset := region.rect.Min.Y
			press := inputEvent{"mouse", fmt.Sprintf("<0;%d;%dM", point.X+1, point.Y-offset+21)}
			release := inputEvent{"mouse", fmt.Sprintf("<0;%d;%dm", point.X+1, point.Y-offset+21)}
			state.click(press, size, doc, offset)
			if state.click(release, size, doc, offset) != "https://example.com" {
				t.Fatalf("scale=%g link hit-test failed", scale)
			}
		}
		previousSize, previousHeight, previousRows, previousBox = size, doc.height, len(rows), box
	}
}

func FuzzScaleOptions(f *testing.F) {
	for _, value := range []string{"0.5", "1", "1.2", "4", "0", "NaN", "Inf", "bad", ""} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		o, err := parseOptions("view", []string{"--scale=" + raw, "doc.md"}, io.Discard)
		v, parseErr := strconv.ParseFloat(raw, 64)
		valid := parseErr == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0.5 && v <= 4
		if (err == nil) != valid {
			t.Fatalf("raw=%q scale=%g err=%v", raw, o.scale, err)
		}
		if err == nil && o.scale != v {
			t.Fatalf("raw=%q scale=%g want=%g", raw, o.scale, v)
		}
	})
}
