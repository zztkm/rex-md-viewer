package main

import (
	"bytes"
	"context"
	"image"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestExternalURL(t *testing.T) {
	for _, value := range []string{
		"https://example.com", "http://localhost:8080/a?b=c&d=e#part",
		"HTTPS://example.com/a%20b?q=%E6%97%A5#%20", "https://example.com/日本語",
		"http://[::1]:8080/", "https://example.com/?q=$(touch%20/tmp/not-executed);x='quoted'",
	} {
		if got, ok := externalURL(value); !ok || got != value {
			t.Errorf("rejected or changed URL %q: %q, %v", value, got, ok)
		}
	}
	for _, value := range []string{
		"", "relative.md", "#part", "//example.com", "file:///tmp/a", "mailto:a@example.com",
		"javascript:alert(1)", "ssh://example.com", "data:text/html,test", "https:relative",
		"https:///path", "https://", "https://?q=x", "https://user@", "https://example.com:bad/",
		" https://example.com", "https://example.com ", "https://example.com/a b",
		"https://example.com/\n", "https://example.com/\x1b]8;;file:///tmp/a",
		"https://example.com/%0a", "https://example.com/%1B", "https://example.com/%7f",
		"https://example.com/%C2%85", "https://example.com/%zz", "https://example.com/\\x",
		"https://example.com/\xff", "https://[not-ip/",
	} {
		if got, ok := externalURL(value); ok {
			t.Errorf("accepted unsafe/unsupported URL %q: %q", value, got)
		}
	}
}

func TestLinkTargetsAndStyles(t *testing.T) {
	source := "# [heading](https://example.com/h)\n\n" +
		"[**bold** and `code`](https://example.com/a?x=1&amp;y=2#part) [ref][r] " +
		"<https://example.com/auto> https://example.com/bare\n\n" +
		"> [quote](https://example.com/q)\n\n- [ ] [pending](https://example.com/p)\n" +
		"- [~] [active](https://example.com/w)\n- [x] [done](https://example.com/d)\n\n" +
		"| a | b |\n| :--- | ---: |\n| [table](https://example.com/t) | [right](https://example.com/right) |\n\n" +
		"[r]: https://example.com/ref\n"
	doc := layoutText(t, source, 500)
	want := []string{"https://example.com/h", "https://example.com/a?x=1&y=2#part",
		"https://example.com/ref", "https://example.com/auto", "https://example.com/bare",
		"https://example.com/q", "https://example.com/p", "https://example.com/w", "https://example.com/d",
		"https://example.com/t", "https://example.com/right"}
	if !reflect.DeepEqual(doc.linkTargets, want) {
		t.Fatalf("targets: %q", doc.linkTargets)
	}
	seen := make(map[int]bool)
	bold, code, completed := false, false, false
	for _, op := range doc.ops {
		if op.style.link == 0 || op.text == "" {
			continue
		}
		st := op.style
		seen[st.link] = true
		bold = bold || st.link == 2 && st.bold
		code = code || st.link == 2 && st.code
		completed = completed || st.link == 9 && st.ink == doc.colors.taskDone
		if !st.underline {
			t.Fatal("link lost underline")
		}
	}
	if len(seen) != len(want) || !bold || !code || !completed {
		t.Fatalf("missing link/style: seen=%v bold=%v code=%v completed=%v", seen, bold, code, completed)
	}
	for _, region := range doc.links {
		p := region.rect.Min.Add(image.Pt(region.rect.Dx()/2, region.rect.Dy()/2))
		if got := doc.linkAt(p); got != region.target {
			t.Fatalf("region %v has hit target %d, want %d", region.rect, got, region.target)
		}
	}
}

func TestUnsupportedLinksAreNotClickable(t *testing.T) {
	source := "[file](file:///tmp/a) [local](README.md) [anchor](#part) [mail](mailto:a@example.com) " +
		"[js](javascript:alert) [bad](https://example.com/%0a) <a@example.com>\n\n" +
		"`https://example.com/inline`\n\n```\nhttps://example.com/code\n```\n\n" +
		"<a href=\"https://example.com/html\">HTML</a>\n\n" +
		"[![loaded](example.png)](https://example.com/image) " +
		"[![failed](missing.png)](https://example.com/failed)"
	doc := layoutText(t, source, 500)
	if len(doc.links) != 0 {
		t.Fatalf("unsupported content has clickable regions: %+v", doc.links)
	}
	for _, text := range []string{"file", "local", "anchor", "mail", "js", "HTML", "画像: failed"} {
		if !strings.Contains(sceneText(doc), text) {
			t.Errorf("lost display text %q", text)
		}
	}
}

func TestLinkRegionsWrapAndAlign(t *testing.T) {
	for _, width := range []int{32, 80, 220, 500} {
		doc := layoutText(t, "[日本語longlonglonglonglong **bold** `code`](https://example.com/wrapped)", width)
		if len(doc.links) == 0 {
			t.Fatalf("no links at width %d", width)
		}
		rows := make(map[int]bool)
		for _, region := range doc.links {
			rows[region.rect.Min.Y] = true
			if !region.rect.In(image.Rect(0, 0, width, doc.height)) {
				t.Fatalf("region outside document: %v", region)
			}
			if doc.linkAt(region.rect.Min) != 1 {
				t.Fatalf("wrapped link is not clickable: %v", region)
			}
		}
		if width < 220 && len(rows) < 2 {
			t.Fatal("link did not wrap")
		}
	}
	for _, alignment := range []string{":---:", "---:"} {
		doc := layoutText(t, "| header |\n| "+alignment+" |\n| [i](https://example.com) |", 500)
		var glyph paintOp
		for _, op := range doc.ops {
			if op.style.link != 0 {
				glyph = op
				break
			}
		}
		if len(doc.links) != 1 || doc.links[0].rect.Min.X != glyph.rect.Min.X || glyph.rect.Min.X < 200 {
			t.Fatalf("link not aligned with table glyph: %v, %v", doc.links, glyph.rect)
		}
	}
}

func TestAdjacentSubcellLinksStayDistinct(t *testing.T) {
	doc := layoutText(t, "[i](https://example.com/a)[l](https://example.com/b)", 500)
	if len(doc.links) != 2 {
		t.Fatalf("regions: %+v", doc.links)
	}
	a, b := doc.links[0], doc.links[1]
	if a.rect.Max.X != b.rect.Min.X {
		t.Fatal("not adjacent")
	}
	if doc.linkAt(image.Pt(a.rect.Max.X-1, a.rect.Min.Y)) != 1 || doc.linkAt(b.rect.Min) != 2 {
		t.Fatal("subcell links share their target")
	}
	if doc.linkAt(image.Pt(a.rect.Min.X-1, a.rect.Min.Y)) != 0 || doc.linkAt(image.Pt(b.rect.Max.X, b.rect.Min.Y)) != 0 {
		t.Fatal("click region expanded into surrounding whitespace")
	}
	// Collecting metadata must not alter the PNG or introduce terminal text.
	before := doc.paint(0, doc.height)
	doc.links, doc.linkTargets = nil, nil
	after := doc.paint(0, doc.height)
	if !bytes.Equal(before.Pix, after.Pix) {
		t.Fatal("link metadata changes pixels")
	}
}

func TestLinkLayoutReplacement(t *testing.T) {
	for _, width := range []int{100, 500} {
		doc := layoutText(t, "[old](https://example.com/old)", width)
		if len(doc.links) == 0 {
			t.Fatal("missing old link")
		}
		next := layoutText(t, "[new](https://example.com/new)", width)
		if len(next.linkTargets) != 1 || next.linkTargets[0] != "https://example.com/new" {
			t.Fatal("old destination survived replacement")
		}
		empty := layoutText(t, "no links", width)
		if len(empty.links) != 0 || len(empty.linkTargets) != 0 {
			t.Fatal("removed link survived replacement")
		}
	}
}

func TestBrowserCommandUsesArgumentsNotShell(t *testing.T) {
	target := "https://example.com/a%20b?q=$(touch%20/tmp/no);x='quoted'&y=2#part"
	cmd, err := browserCommand(context.Background(), target)
	if runtime.GOOS != "darwin" {
		if err == nil {
			t.Fatal("non-macOS unexpectedly supports browser launch")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cmd.Args, []string{"/usr/bin/open", "-u", target}) || cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Fatalf("unsafe command: %+v", cmd)
	}
	for _, target := range []string{"file:///tmp/a", "javascript:alert(1)", "https://example.com/\x1b"} {
		if _, err := browserCommand(context.Background(), target); err == nil {
			t.Fatalf("unsafe URL reaches launcher: %q", target)
		}
	}
}
