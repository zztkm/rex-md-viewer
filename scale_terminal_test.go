package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestScaleViewerHelper(t *testing.T) {
	file := os.Getenv("REX_MD_SCALE_TEST_FILE")
	if file == "" {
		return
	}
	raw := os.Getenv("REX_MD_SCALE_TEST_VALUE")
	multiplier, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatal(err)
	}
	o, err := parseOptions("view", []string{"--in-place", "--theme", "light", "--scale", raw, "--", file}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	fonts, err := loadFonts("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer fonts.close()
	initial, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(file + ".updated")
	if err != nil {
		t.Fatal(err)
	}
	step := max(1, int(24*0.9*multiplier))
	for _, frame := range []struct {
		name                  string
		width, height, offset int
		baseScale             float64
		data                  []byte
	}{
		{"initial", 640, 396, 0, 0.9, initial},
		{"scroll", 640, 396, step, 0.9, initial},
		{"page", 640, 396, step + 396, 0.9, initial},
		{"small-cells", 640, 176, 0, 0.5, initial},
		{"resize", 480, 324, 0, 0.9, initial},
		{"updated", 480, 324, 0, 0.9, updated},
	} {
		doc, err := layoutMarkdown(frame.data, file, frame.width, frame.baseScale*multiplier, fonts, paletteFor("light"))
		if err != nil {
			t.Fatal(err)
		}
		if err := writePNG(file+"."+frame.name+".png", doc.paint(frame.offset, frame.height)); err != nil {
			t.Fatal(err)
		}
	}
	if err := view(o, initial, fonts); err != nil {
		t.Fatal(err)
	}
}

func TestScaleTerminalIntegration(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the PTY integration test")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, filepath.Join("scripts", "test_scale_terminal.py"), binary)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PTY scale integration: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
