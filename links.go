package main

import (
	"context"
	"fmt"
	"image"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Keep the original URL, including its query, fragment and percent escapes.
// Never turn an invalid or non-web destination into a file or shell argument.
func externalURL(destination string) (string, bool) {
	if !utf8.ValidString(destination) || strings.IndexFunc(destination, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r) || r == '\\'
	}) >= 0 {
		return "", false
	}
	u, err := url.Parse(destination)
	if err != nil || u.Opaque != "" || u.Host == "" || u.Hostname() == "" ||
		(!strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")) {
		return "", false
	}
	decoded, err := url.PathUnescape(destination)
	if err != nil || strings.IndexFunc(decoded, unicode.IsControl) >= 0 {
		return "", false
	}
	return destination, true
}

func (l *layout) linkStyle(st textStyle, destination string) textStyle {
	st.underline, st.ink, st.link = true, l.doc.colors.accent, 0
	if target, ok := externalURL(destination); ok {
		l.doc.linkTargets = append(l.doc.linkTargets, target)
		st.link = len(l.doc.linkTargets)
	}
	return st
}

type linkRegion struct {
	rect   image.Rectangle
	target int
}

// Derive regions after alignment and task/table layout have moved the glyphs.
// Use font metrics rather than paintOp's deliberately oversized culling box.
func (d *document) collectLinks() {
	d.links = nil
	for _, op := range d.ops {
		if op.style.link == 0 || op.text == "" || op.rect.Dx() == 0 {
			continue
		}
		st := op.style
		ch := []rune(op.text)[0]
		metrics := d.fonts.runeFace(st.size, st.bold, st.code && ch < 128, ch).Metrics()
		baseline := st.baseline
		if baseline == 0 {
			baseline = d.fonts.baseline(st.size)
		}
		r := image.Rect(op.rect.Min.X, op.rect.Min.Y+max(0, baseline-metrics.Ascent.Ceil()),
			op.rect.Max.X, op.rect.Min.Y+baseline+max(metrics.Descent.Ceil(), 2+max(1, st.size/16)))
		if st.code {
			top, height := op.rect.Min.Y, st.size*3/2
			if st.lineHeight > 0 {
				top += max(0, baseline-st.size)
				height = st.lineHeight
			}
			r = r.Union(image.Rect(r.Min.X, top, r.Max.X, top+height))
		}
		r = r.Intersect(image.Rect(0, 0, d.width, d.height))
		if r.Empty() {
			continue
		}
		if n := len(d.links); n > 0 {
			last := &d.links[n-1]
			if last.target == st.link && last.rect.Max.X == r.Min.X &&
				last.rect.Min.Y == r.Min.Y && last.rect.Max.Y == r.Max.Y {
				last.rect.Max.X = r.Max.X
				continue
			}
		}
		d.links = append(d.links, linkRegion{r, st.link})
	}
}

func (d *document) linkAt(p image.Point) int {
	found := 0
	for _, link := range d.links {
		if p.In(link.rect) {
			if found != 0 && found != link.target {
				return 0 // Ambiguous geometry must never open the wrong URL.
			}
			found = link.target
		}
	}
	return found
}

func browserCommand(ctx context.Context, destination string) (*exec.Cmd, error) {
	target, ok := externalURL(destination)
	if !ok {
		return nil, fmt.Errorf("cannot open this link destination")
	}
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("opening external links is only supported on macOS")
	}
	// /usr/bin/open delegates http(s) to the default browser. No shell, stdin,
	// terminal output, or Markdown-supplied executable is involved.
	return exec.CommandContext(ctx, "/usr/bin/open", "-u", target), nil
}

func openExternalURL(ctx context.Context, destination string) error {
	cmd, err := browserCommand(ctx, destination)
	if err != nil {
		return err
	}
	return cmd.Run()
}
