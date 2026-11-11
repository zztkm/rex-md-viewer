package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"
)

const maxMarkdownBytes = 8 << 20
const pollInterval = 200 * time.Millisecond

func readMarkdown(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxMarkdownBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxMarkdownBytes {
		return nil, fmt.Errorf("Markdown file must be 8 MiB or smaller")
	}
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("file is not valid UTF-8: %s", path)
	}
	return b, nil
}

type snapshot struct {
	data    []byte
	err     error
	version uint64
	forced  bool
}
type fileMonitor struct {
	updates chan snapshot
	refresh chan struct{}
}

func (m *fileMonitor) reload() {
	select {
	case m.refresh <- struct{}{}:
	default:
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// debounceState is independent of the filesystem, so replacement/recovery and
// the maximum debounce latency can be tested without sleeps.
type debounceState struct {
	data        []byte
	err         string
	pending     bool
	first, last time.Time
	version     uint64
}

func (s *debounceState) observe(data []byte, err error, now time.Time) bool {
	e := errorText(err)
	if bytes.Equal(s.data, data) && s.err == e {
		return false
	}
	s.data, s.err = data, e
	s.version++
	if !s.pending {
		s.first = now
	}
	s.last, s.pending = now, true
	return true
}
func (s *debounceState) due(now time.Time) bool {
	return s.pending && (now.Sub(s.last) >= pollInterval || now.Sub(s.first) >= time.Second)
}

func startMonitor(ctx context.Context, path string, initial []byte) *fileMonitor {
	m := &fileMonitor{updates: make(chan snapshot, 1), refresh: make(chan struct{}, 1)}
	go func() {
		defer close(m.updates)
		state := debounceState{data: initial}
		tick := time.NewTicker(pollInterval)
		defer tick.Stop()
		for {
			var now time.Time
			forced := false
			select {
			case <-ctx.Done():
				return
			case now = <-tick.C:
			case <-m.refresh:
				now, forced = time.Now(), true
			}
			b, err := readMarkdown(path)
			changed := state.observe(b, err, now)
			if forced && !changed {
				state.version++
			}
			if !forced && !state.due(now) {
				continue
			}
			ev := snapshot{data: state.data, err: err, version: state.version, forced: forced}
			// A single reader owns both manual and automatic observations. A
			// queued pre-reload result can never overwrite a manual reload.
			select {
			case <-m.updates:
			default:
			}
			select {
			case m.updates <- ev:
			case <-ctx.Done():
				return
			}
			state.pending = false
		}
	}()
	return m
}
