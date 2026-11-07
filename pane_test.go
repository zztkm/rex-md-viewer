package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakeRexState struct {
	Blocks         []rexBlock
	Splits, Closes int
	SplitArgs      []string
}

func TestFakeRexProcess(t *testing.T) {
	if os.Getenv("REX_MD_TEST_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	if len(args) < 7 {
		os.Exit(9)
	}
	// The application always pins the endpoint and disables autostart.
	if args[0] != "-S" || args[1] != "unix:///fake rex.sock" || args[2] != "--autostart=false" {
		os.Exit(9)
	}
	args = args[5:]
	statePath := os.Getenv("REX_MD_TEST_STATE")
	var state fakeRexState
	if data, e := os.ReadFile(statePath); e == nil {
		json.Unmarshal(data, &state)
	} else {
		state.Blocks = []rexBlock{{ID: "block:anchor", Label: "agent"}}
	}
	writeState := func() { data, _ := json.Marshal(state); os.WriteFile(statePath, data, 0600) }
	value := func(flag string) string {
		for i, arg := range args {
			if arg == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	if os.Getenv("REX_MD_TEST_FAIL") == args[0] || os.Getenv("REX_MD_TEST_FAIL") == strings.Join(args[:min(2, len(args))], " ") {
		fmt.Fprintln(os.Stderr, "simulated connection failure")
		os.Exit(1)
	}
	switch args[0] {
	case "split":
		state.Splits++
		state.SplitArgs = append([]string(nil), args...)
		id := "block:viewer" + strconv.Itoa(state.Splits)
		label := value("--label")
		state.Blocks = append(state.Blocks, rexBlock{ID: id, Label: label})
		token := strings.TrimPrefix(label, "mdv:")
		dir, _ := stateDir()
		os.MkdirAll(dir, 0700)
		data, _ := json.Marshal(paneReady{Block: id, Token: token, PID: 4321})
		os.WriteFile(readyPath(dir, token), data, 0600)
		writeState()
		if os.Getenv("REX_MD_TEST_FAIL") == "split-after-create" {
			os.Exit(1)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"block_ids": []string{"block:anchor", id}})
	case "block":
		switch args[1] {
		case "ls":
			json.NewEncoder(os.Stdout).Encode(map[string]any{"blocks": state.Blocks})
		case "call":
			exe, _ := os.Executable()
			json.NewEncoder(os.Stdout).Encode(map[string]any{"last_exit": nil, "foreground": map[string]any{"pid": 4321, "path": exe}})
		case "move":
			fmt.Print("{}")
		case "close":
			id := args[len(args)-1]
			state.Closes++
			for i, b := range state.Blocks {
				if b.ID == id {
					state.Blocks = append(state.Blocks[:i], state.Blocks[i+1:]...)
					break
				}
			}
			writeState()
			fmt.Print("{}")
		default:
			os.Exit(8)
		}
	default:
		os.Exit(7)
	}
	os.Exit(0)
}

func setupFakeRex(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// No user input is interpolated in this test wrapper; the executable is quoted.
	wrapper := "#!/bin/sh\nexec " + strconv.Quote(exe) + " -test.run=^TestFakeRexProcess$ -- \"$@\"\n"
	if err = os.WriteFile(filepath.Join(dir, "rex"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state home"))
	t.Setenv("REX_SERVER", "unix:///fake rex.sock")
	t.Setenv("REX_SESSION", "session:test")
	t.Setenv("REX_BLOCK", "block:anchor")
	t.Setenv("REX_MD_TEST_HELPER", "1")
	state := filepath.Join(dir, "fake.json")
	t.Setenv("REX_MD_TEST_STATE", state)
	return state
}
func readFakeState(t *testing.T, path string) fakeRexState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state fakeRexState
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
func TestPaneConcurrentReuseAndHide(t *testing.T) {
	path := setupFakeRex(t)
	file := "/tmp/日本語 ' ${x}; $(no).md"
	o := options{file: file, ratio: 40, theme: "dark"}
	var wg sync.WaitGroup
	outs := make([]bytes.Buffer, 3)
	errs := make([]error, 3)
	for i := range outs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = managePane(file, o, false, &outs[i]) }(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		if outs[i].String() != "block:viewer1\n" {
			t.Fatal(outs[i].String())
		}
	}
	state := readFakeState(t, path)
	if state.Splits != 1 {
		t.Fatalf("duplicate splits: %d", state.Splits)
	}
	if state.SplitArgs[len(state.SplitArgs)-1] != file {
		t.Fatalf("path changed: %q", state.SplitArgs)
	}
	for _, want := range []string{"--focus=false", "--shell=none", "--in-place"} {
		found := false
		for _, a := range state.SplitArgs {
			found = found || a == want
		}
		if !found {
			t.Fatalf("missing %s", want)
		}
	}
	if err := managePane(file, options{}, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := managePane(file, options{}, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if state = readFakeState(t, path); state.Closes != 1 {
		t.Fatalf("closes=%d", state.Closes)
	}
}
func TestPaneDoesNotCloseUnrelatedBlock(t *testing.T) {
	path := setupFakeRex(t)
	file := "/tmp/doc"
	o := options{file: file, ratio: 40, theme: "dark"}
	if err := managePane(file, o, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	state := readFakeState(t, path)
	state.Blocks[1].Label = "unrelated shell"
	data, _ := json.Marshal(state)
	os.WriteFile(path, data, 0600)
	if err := managePane(file, options{}, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if readFakeState(t, path).Closes != 0 {
		t.Fatal("closed unrelated pane")
	}
}
func TestPaneConnectionFailureKeepsRecord(t *testing.T) {
	setupFakeRex(t)
	file := "/tmp/doc"
	o := options{file: file, ratio: 40, theme: "dark"}
	if err := managePane(file, o, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	r, _ := getRexContext()
	record := filepath.Join(r.dir, paneKey(r, file)+".json")
	before, _ := os.ReadFile(record)
	t.Setenv("REX_MD_TEST_FAIL", "block call")
	if err := managePane(file, options{}, true, io.Discard); err == nil {
		t.Fatal("ignored connection failure")
	}
	after, _ := os.ReadFile(record)
	if !bytes.Equal(before, after) {
		t.Fatal("record removed on connection failure")
	}
}
func TestPaneRecoversTimedOutSplit(t *testing.T) {
	path := setupFakeRex(t)
	t.Setenv("REX_MD_TEST_FAIL", "split-after-create")
	file := "/tmp/doc"
	if err := managePane(file, options{file: file, ratio: 40, theme: "dark"}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if readFakeState(t, path).Splits != 1 {
		t.Fatal("split was retried")
	}
}
func TestPaneKeyScopesAndEnvironment(t *testing.T) {
	r := rexContext{server: "s", session: "session", anchor: "pane"}
	key := paneKey(r, "/tmp/a")
	for _, other := range []rexContext{{server: "s2", session: r.session, anchor: r.anchor}, {server: r.server, session: "other", anchor: r.anchor}, {server: r.server, session: r.session, anchor: "other"}} {
		if paneKey(other, "/tmp/a") == key {
			t.Fatal("identity collision")
		}
	}
	if reflect.DeepEqual(key, paneKey(r, "/tmp/b")) {
		t.Fatal("file not scoped")
	}
	t.Setenv("REX_SERVER", "")
	if _, err := getRexContext(); err == nil {
		t.Fatal("Rex absence accepted")
	}
}
