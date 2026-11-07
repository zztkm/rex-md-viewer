package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseOptions(t *testing.T) {
	cases := []struct {
		name, command string
		args          []string
		wantErr       bool
	}{
		{"split", "view", []string{"README.md"}, false},
		{"in-place", "view", []string{"--in-place", "日本語 ファイル.md"}, false},
		{"below", "view", []string{"--below", "--ratio", "30", "doc.md"}, false},
		{"conflict-below", "view", []string{"--in-place", "--below", "doc.md"}, true},
		{"conflict-explicit-default", "view", []string{"--in-place", "--ratio", "40", "doc.md"}, true},
		{"conflict-false", "view", []string{"--in-place", "--below=false", "doc.md"}, true},
		{"ratio0", "view", []string{"--ratio", "0", "doc.md"}, true},
		{"ratio100", "view", []string{"--ratio", "100", "doc.md"}, true},
		{"ratio-invalid", "view", []string{"--ratio", "bad", "doc.md"}, true},
		{"missing-file", "view", nil, true},
		{"extra-file", "view", []string{"one", "two"}, true},
		{"unknown-option", "view", []string{"--unknown", "one"}, true},
		{"terminal-theme", "view", []string{"--theme", "terminal", "doc"}, false},
		{"bad-theme", "view", []string{"--theme", "no", "doc"}, true},
		{"render", "render", []string{"-o", "out.png", "doc"}, false},
		{"render-missing-output", "render", []string{"doc"}, true},
		{"render-terminal", "render", []string{"--theme", "terminal", "-o", "out", "doc"}, true},
		{"render-width0", "render", []string{"--width", "0", "-o", "out", "doc"}, true},
		{"render-width-limit", "render", []string{"--width", "8193", "-o", "out", "doc"}, true},
		{"dash-path", "view", []string{"--", "-日本語.md"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, err := parseOptions(tc.command, tc.args, io.Discard)
			if (err != nil) != tc.wantErr {
				t.Fatalf("options=%+v err=%v", o, err)
			}
			if tc.wantErr {
				var u *usageError
				if !errors.As(err, &u) {
					t.Fatalf("not usage error: %T", err)
				}
			}
		})
	}
	o, _ := parseOptions("view", []string{"doc"}, io.Discard)
	if o.inPlace || o.ratio != 40 || o.theme != "terminal" {
		t.Fatalf("defaults: %+v", o)
	}
	o, _ = parseOptions("render", []string{"-o", "out", "doc"}, io.Discard)
	if o.width != 1000 || o.theme != "light" {
		t.Fatalf("render defaults: %+v", o)
	}
}

func TestChildArgsPreservePaths(t *testing.T) {
	o := options{file: "/tmp/日本語 '$() ;.md", font: "/font body.ttf", bold: "/font bold.ttf", theme: "dark"}
	want := []string{"/path to/self", "view", "--in-place", "--theme", "dark", "--font", o.font, "--font-bold", o.bold, "--", o.file}
	if got := childArgs("/path to/self", o); !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}
func TestHelpAndUnknownCommand(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"view", "--help"}, {"render", "--help"}, {"hide", "--help"}, {"skill", "--help"}, {"skill", "install", "--help"}, {"--version"}} {
		var out bytes.Buffer
		if err := run(args, &out); err != nil || out.Len() == 0 {
			t.Fatalf("%v: %v %q", args, err, out.String())
		}
	}
	if err := run([]string{"show", "README.md"}, io.Discard); err == nil {
		t.Fatal("old show command accepted")
	}
}
func TestSkillInstall(t *testing.T) {
	dir := t.TempDir()
	args := []string{"install", "--dir", dir}
	if err := runSkill(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rex-md-viewer", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, skillContent) {
		t.Fatalf("%s %v", data, err)
	}
	if err = runSkill(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("custom"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = runSkill(args, io.Discard); err == nil {
		t.Fatal("overwrote without force")
	}
	if err = runSkill(append(args, "--force"), io.Discard); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = runSkill([]string{"show"}, &out); err != nil || !bytes.Equal(out.Bytes(), skillContent) {
		t.Fatal("skill show differs")
	}
	if err = runSkill([]string{"install"}, io.Discard); err == nil {
		t.Fatal("missing --dir accepted")
	}
}
