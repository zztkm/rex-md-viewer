package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This seam exists only in the test executable. Tests never launch a browser or
// let a runtime environment variable replace the production URL opener.
func TestLinkViewerHelper(t *testing.T) {
	file := os.Getenv("REX_MD_LINK_TEST_FILE")
	if file == "" {
		return
	}
	fonts, err := loadFonts("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer fonts.close()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	size, err := getSize(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := layoutMarkdown(data, file, size.width, max(0.5, float64(size.height)/float64(size.rows)/20), fonts, paletteFor("dark"))
	if err != nil {
		t.Fatal(err)
	}
	var points [][2]int
	for _, region := range doc.links {
		points = append(points, [2]int{region.rect.Min.X + region.rect.Dx()/2, region.rect.Min.Y + region.rect.Dy()/2})
	}
	encoded, _ := json.Marshal(points)
	if err := os.WriteFile(file+".points", encoded, 0600); err != nil {
		t.Fatal(err)
	}
	opener := func(ctx context.Context, target string) error {
		f, err := os.OpenFile(file+".opened", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = json.NewEncoder(f).Encode(target)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if strings.HasSuffix(target, "/fail") {
			return fmt.Errorf("test browser failure")
		}
		if strings.HasSuffix(target, "/slow") {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
		return nil
	}
	if err := viewWithOpener(options{file: file, theme: "dark"}, data, fonts, opener); err != nil {
		t.Fatal(err)
	}
}

func TestLinkViewerTerminalIntegration(t *testing.T) {
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
	cmd := exec.CommandContext(ctx, python, filepath.Join("scripts", "test_links_terminal.py"), binary)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PTY link integration: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
