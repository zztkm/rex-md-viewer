package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDebounce(t *testing.T) {
	now := time.Unix(10, 0)
	s := debounceState{data: []byte("old")}
	if s.observe([]byte("old"), nil, now) {
		t.Fatal("unchanged")
	}
	if !s.observe([]byte("new"), nil, now) {
		t.Fatal("change missed")
	}
	if s.due(now.Add(199 * time.Millisecond)) {
		t.Fatal("debounce early")
	}
	if !s.due(now.Add(200 * time.Millisecond)) {
		t.Fatal("debounce late")
	}
	for i := 1; i <= 5; i++ {
		s.observe([]byte{byte(i)}, nil, now.Add(time.Duration(i)*200*time.Millisecond))
	}
	if !s.due(now.Add(time.Second)) {
		t.Fatal("continuous writes starved rendering")
	}
	if s.version != 6 {
		t.Fatalf("version %d", s.version)
	}
}
func TestReadMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "日本語 ' ${x}.md")
	if err := os.WriteFile(path, []byte("日本語"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readMarkdown(path); err != nil || string(got) != "日本語" {
		t.Fatalf("%q %v", got, err)
	}
	for _, data := range [][]byte{{0xff}, bytes.Repeat([]byte{'a'}, maxMarkdownBytes+1)} {
		os.WriteFile(path, data, 0600)
		if _, err := readMarkdown(path); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := readMarkdown(dir); err == nil {
		t.Fatal("directory accepted")
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := readMarkdown(path); err != nil || len(b) != 0 {
		t.Fatal("empty rejected")
	}
}
func awaitSnapshot(t *testing.T, m *fileMonitor) snapshot {
	t.Helper()
	select {
	case ev := <-m.updates:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no snapshot")
		return snapshot{}
	}
}
func TestMonitorReplacementSameMetadataAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	os.WriteFile(path, []byte("old"), 0600)
	info, _ := os.Stat(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := startMonitor(ctx, path, []byte("old"))
	start := time.Now()
	os.WriteFile(path, []byte("new"), 0600)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	ev := awaitSnapshot(t, m)
	if string(ev.data) != "new" || ev.err != nil {
		t.Fatalf("%+v", ev)
	}
	t.Logf("same-size, same-mtime detection: %s", time.Since(start))
	tmp := filepath.Join(dir, "save.tmp")
	os.WriteFile(tmp, []byte("replacement"), 0600)
	os.Rename(tmp, path)
	if ev = awaitSnapshot(t, m); string(ev.data) != "replacement" || ev.err != nil {
		t.Fatalf("%+v", ev)
	}
	os.Remove(path)
	if ev = awaitSnapshot(t, m); ev.err == nil {
		t.Fatal("deletion missed")
	}
	os.WriteFile(path, []byte("replacement"), 0600)
	if ev = awaitSnapshot(t, m); ev.err != nil || string(ev.data) != "replacement" {
		t.Fatalf("recovery: %+v", ev)
	}
	select {
	case ev := <-m.updates:
		t.Fatalf("unchanged contents emitted: %+v", ev)
	case <-time.After(450 * time.Millisecond):
	}
}
func TestMonitorManualReloadOrdersSnapshots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	os.WriteFile(path, []byte("initial"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := startMonitor(ctx, path, []byte("initial"))
	m.reload()
	first := awaitSnapshot(t, m)
	if !first.forced || string(first.data) != "initial" {
		t.Fatalf("%+v", first)
	}
	os.WriteFile(path, []byte("manual"), 0600)
	m.reload()
	next := awaitSnapshot(t, m)
	if !next.forced || next.version <= first.version || string(next.data) != "manual" {
		t.Fatalf("%+v", next)
	}
	select {
	case ev := <-m.updates:
		t.Fatalf("an older automatic observation overwrote reload: %+v", ev)
	case <-time.After(450 * time.Millisecond):
	}
}

func TestMonitorFollowsSymlinkPath(t *testing.T) {
	dir := t.TempDir()
	a, b, link := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "link")
	os.WriteFile(a, []byte("a"), 0600)
	os.WriteFile(b, []byte("b"), 0600)
	os.Symlink(a, link)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := startMonitor(ctx, link, []byte("a"))
	os.Remove(link)
	os.Symlink(b, link)
	ev := awaitSnapshot(t, m)
	if string(ev.data) != "b" {
		t.Fatalf("%+v", ev)
	}
}
