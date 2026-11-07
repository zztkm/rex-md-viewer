package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type paneRecord struct {
	Block      string `json:"block"`
	Token      string `json:"token"`
	Executable string `json:"executable"`
}
type paneReady struct {
	Block string `json:"block"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}
type rexBlock struct {
	ID    string `json:"block_id"`
	Label string `json:"label"`
}
type rexContext struct{ server, session, anchor, dir string }

func stateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Abs(filepath.Join(base, "rex-md-viewer"))
}
func getRexContext() (rexContext, error) {
	r := rexContext{server: os.Getenv("REX_SERVER"), session: os.Getenv("REX_SESSION"), anchor: os.Getenv("REX_BLOCK")}
	if r.server == "" || r.session == "" || r.anchor == "" {
		return r, fmt.Errorf("Rex 内で実行してください（REX_SERVER / REX_SESSION / REX_BLOCK が必要です）")
	}
	if _, err := exec.LookPath("rex"); err != nil {
		return r, fmt.Errorf("rex CLI が PATH にありません")
	}
	var err error
	r.dir, err = stateDir()
	return r, err
}
func (r rexContext) command(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	// An explicit endpoint prevents the CLI from falling back to another server.
	full := append([]string{"-S", r.server, "--autostart=false", "--timeout", "10s"}, args...)
	cmd := exec.CommandContext(ctx, "rex", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("rex %s: %w: %s", args[0], err, safeText(strings.TrimSpace(string(out))))
	}
	return out, nil
}
func (r rexContext) blocks() ([]rexBlock, error) {
	out, err := r.command("block", "ls", "-s", r.session, "--json")
	if err != nil {
		return nil, err
	}
	var result struct {
		Blocks []rexBlock `json:"blocks"`
	}
	if err = json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("rex block ls の応答: %w", err)
	}
	return result.Blocks, nil
}
func paneKey(r rexContext, file string) string {
	b, _ := json.Marshal([]string{r.server, r.session, r.anchor, file})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func readyPath(dir, token string) string { return filepath.Join(dir, "ready-"+token+".json") }
func validToken(token string) bool {
	b, err := hex.DecodeString(token)
	return err == nil && len(b) == 16
}

// Process identity plus a random per-launch label prevents closing a shell or
// another program in a retained/reused pane. A CLI failure is NOT absence.
func (r rexContext) owns(rec paneRecord, blocks []rexBlock) (bool, error) {
	if !validToken(rec.Token) || rec.Executable == "" {
		return false, nil
	}
	found := false
	for _, b := range blocks {
		if b.ID == rec.Block && b.Label == "mdv:"+rec.Token {
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	data, err := os.ReadFile(readyPath(r.dir, rec.Token))
	var ready paneReady
	if err == nil {
		if err = json.Unmarshal(data, &ready); err != nil {
			return false, err
		}
		if ready.Token != rec.Token || ready.Block != rec.Block {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	// Missing readiness can mean the process is still starting. Keep ownership
	// so a retry waits for it instead of opening a second pane.
	out, err := r.command("block", "call", "-s", r.session, "-b", rec.Block, "process")
	if err != nil {
		return false, err
	}
	var result struct {
		LastExit   json.RawMessage `json:"last_exit"`
		Foreground *struct {
			PID  int    `json:"pid"`
			Path string `json:"path"`
		} `json:"foreground"`
		Child *struct {
			PID  int    `json:"pid"`
			Path string `json:"path"`
		} `json:"child"`
	}
	if err = json.Unmarshal(out, &result); err != nil {
		return false, err
	}
	if len(result.LastExit) != 0 && string(result.LastExit) != "null" {
		return false, nil
	}
	sameExecutable := func(path string) bool {
		resolved, err := filepath.EvalSymlinks(path)
		return err == nil && resolved == rec.Executable
	}
	matches := result.Foreground != nil && (ready.PID == 0 || result.Foreground.PID == ready.PID) && sameExecutable(result.Foreground.Path)
	matches = matches || (result.Child != nil && (ready.PID == 0 || result.Child.PID == ready.PID) && sameExecutable(result.Child.Path))
	return matches, nil
}

func managePane(file string, o options, hide bool, out io.Writer) error {
	r, err := getRexContext()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(r.dir, 0700); err != nil {
		return err
	}
	key := paneKey(r, file)
	path := filepath.Join(r.dir, key+".json")
	lock, err := os.OpenFile(filepath.Join(r.dir, key+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	blocks, err := r.blocks()
	if err != nil {
		return err
	}
	anchorFound := false
	for _, b := range blocks {
		if b.ID == r.anchor {
			anchorFound = true
		}
	}
	if !anchorFound {
		return fmt.Errorf("呼び出し元の Rex ペインが存在しません: %s", r.anchor)
	}
	var rec paneRecord
	if data, e := os.ReadFile(path); e == nil {
		if e = json.Unmarshal(data, &rec); e != nil {
			return fmt.Errorf("状態ファイルが壊れています: %w", e)
		}
		if rec.Block == "" && validToken(rec.Token) {
			for _, b := range blocks {
				if b.Label == "mdv:"+rec.Token {
					rec.Block = b.ID
					break
				}
			}
			if rec.Block != "" {
				data, _ := json.Marshal(rec)
				if e = atomicWrite(path, data, 0600); e != nil {
					return e
				}
			}
		}
		owned, e := r.owns(rec, blocks)
		if e != nil {
			return e
		}
		if owned {
			if !hide {
				return r.waitReady(rec, out)
			}
			if _, e = r.command("block", "close", "-s", r.session, rec.Block); e != nil {
				return e
			}
			if e = os.Remove(path); e != nil {
				return e
			}
			os.Remove(readyPath(r.dir, rec.Token))
			return nil
		}
		if e = os.Remove(path); e != nil {
			return e
		}
		if validToken(rec.Token) {
			os.Remove(readyPath(r.dir, rec.Token))
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if hide {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}
	var tokenBytes [16]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes[:])
	rec = paneRecord{Token: token, Executable: self}
	direction := "right"
	if o.below {
		direction = "below"
	}
	env, err := exec.LookPath("env")
	if err != nil {
		return err
	}
	args := []string{"split", "-s", r.session, "-b", r.anchor, "--split", direction, "--ratio", strconv.Itoa(o.ratio), "--focus=false", "--shell=none", "--label", "mdv:" + token, "--json", "--", env, "REX_MD_VIEWER_TOKEN=" + token, "XDG_STATE_HOME=" + filepath.Dir(r.dir), fontEnv + "=" + o.font, boldFontEnv + "=" + o.bold}
	args = append(args, childArgs(self, o)...)
	pending, _ := json.Marshal(rec)
	if err = atomicWrite(path, pending, 0600); err != nil {
		return err
	}
	result, err := r.command(args...)
	if err != nil {
		// Do not retry a timed-out split blindly. Recover its block ID by nonce.
		if now, e := r.blocks(); e == nil {
			for _, b := range now {
				if b.Label == "mdv:"+token {
					rec.Block = b.ID
					break
				}
			}
		}
		if rec.Block == "" {
			return err
		}
	} else {
		var created struct {
			Blocks []string `json:"block_ids"`
		}
		if e := json.Unmarshal(result, &created); e != nil {
			return fmt.Errorf("rex split の応答: %w", e)
		}
		for _, id := range created.Blocks {
			if id != r.anchor {
				rec.Block = id
				break
			}
		}
		if rec.Block == "" {
			return fmt.Errorf("rex split が新しいペイン ID を返しませんでした")
		}
	}
	data, _ := json.Marshal(rec)
	if err = atomicWrite(path, data, 0600); err != nil {
		r.command("block", "close", "-s", r.session, rec.Block)
		return err
	}
	// Some Rex releases ignore split --ratio; move applies the requested ratio.
	if _, err = r.command("block", "move", "-s", r.session, rec.Block, direction, r.anchor, "--ratio", strconv.Itoa(o.ratio)); err != nil {
		return err
	}
	return r.waitReady(rec, out)
}

func (r rexContext) waitReady(rec paneRecord, out io.Writer) error {
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		data, e := os.ReadFile(readyPath(r.dir, rec.Token))
		if e == nil {
			var ready paneReady
			if json.Unmarshal(data, &ready) == nil && ready.Token == rec.Token && ready.Block == rec.Block {
				fmt.Fprintln(out, rec.Block)
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	// This block was just created by this call; leave an error visible for
	// inspection rather than claiming that an unsuccessful viewer opened.
	return fmt.Errorf("Viewer の起動を確認できませんでした（%s）。rex capture -s %s -b %s でエラーを確認してください", rec.Block, r.session, rec.Block)
}

func reportReady() error {
	token := os.Getenv("REX_MD_VIEWER_TOKEN")
	if token == "" {
		return nil
	}
	if !validToken(token) {
		return fmt.Errorf("不正な Viewer 起動トークン")
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(paneReady{Block: os.Getenv("REX_BLOCK"), Token: token, PID: os.Getpid()})
	return atomicWrite(readyPath(dir, token), data, 0600)
}
