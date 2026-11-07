package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const graphicsQueryID = 2147483000
const terminalColorQuery = "\x1b]10;?\x1b\\\x1b]11;?\x1b\\\x1b]4;4;?\x1b\\"

type terminalSize struct{ cols, rows, width, height int }

func getSize(fd int) (terminalSize, error) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return terminalSize{}, err
	}
	if ws.Col == 0 || ws.Row < 3 {
		return terminalSize{}, fmt.Errorf("端末は 1 列以上、3 行以上必要です")
	}
	s := terminalSize{int(ws.Col), int(ws.Row), int(ws.Xpixel), int(ws.Ypixel)}
	if s.width == 0 {
		s.width = s.cols * 8
	}
	if s.height == 0 {
		s.height = s.rows * 18
	}
	if s.width > 8192 || int64(s.width)*int64(s.height) > maxRenderPixels {
		return s, fmt.Errorf("端末の描画領域が大きすぎます")
	}
	return s, nil
}

type inputEvent struct{ kind, value string }
type inputDecoder struct{ pending []byte }

func (d *inputDecoder) feed(data []byte) []inputEvent {
	d.pending = append(d.pending, data...)
	var events []inputEvent
	for len(d.pending) > 0 {
		b := d.pending
		if b[0] != 27 {
			d.pending = b[1:]
			switch b[0] {
			case 'q', 3:
				events = append(events, inputEvent{kind: "quit"})
			case 'j', 'k', ' ', 'b', 'g', 'G', 'r':
				events = append(events, inputEvent{kind: "key", value: string(b[:1])})
			}
			continue
		}
		if len(b) < 2 {
			break
		}
		if b[1] == ']' || b[1] == '_' {
			end := -1
			length := 0
			for i := 2; i < len(b); i++ {
				if b[1] == ']' && b[i] == 7 {
					end = i
					length = 1
					break
				}
				if b[i] == 27 && i+1 < len(b) && b[i+1] == '\\' {
					end = i
					length = 2
					break
				}
			}
			if end < 0 {
				if len(b) > 8192 {
					d.pending = nil
				}
				break
			}
			kind := "graphics"
			if b[1] == ']' {
				kind = "color"
			}
			value := string(b[2:end])
			if kind == "graphics" {
				value = strings.TrimPrefix(value, "G")
			}
			events = append(events, inputEvent{kind: kind, value: value})
			d.pending = b[end+length:]
			continue
		}
		if b[1] == '[' {
			end := -1
			for i := 2; i < len(b); i++ {
				if b[i] >= 0x40 && b[i] <= 0x7e {
					end = i
					break
				}
			}
			if end < 0 {
				if len(b) > 256 {
					d.pending = nil
				}
				break
			}
			seq := string(b[2 : end+1])
			d.pending = b[end+1:]
			keys := map[string]string{"A": "k", "B": "j", "H": "g", "F": "G", "1~": "g", "4~": "G", "7~": "g", "8~": "G", "5~": "b", "6~": " "}
			if key, ok := keys[seq]; ok {
				events = append(events, inputEvent{kind: "key", value: key})
			}
			if strings.HasPrefix(seq, "<") && strings.HasSuffix(seq, "M") {
				parts := strings.Split(strings.TrimSuffix(seq[1:], "M"), ";")
				if len(parts) == 3 {
					code, _ := strconv.Atoi(parts[0])
					if code&64 != 0 {
						key := "k"
						if code&1 != 0 {
							key = "j"
						}
						events = append(events, inputEvent{kind: "key", value: key})
					}
				}
			}
			continue
		}
		// Unknown escape or SS3 key: consume it without interpreting reply bytes
		// as keystrokes. In particular an APC containing "q" cannot quit.
		if b[1] == 'O' {
			if len(b) < 3 {
				break
			}
			switch b[2] {
			case 'H':
				events = append(events, inputEvent{kind: "key", value: "g"})
			case 'F':
				events = append(events, inputEvent{kind: "key", value: "G"})
			}
			d.pending = b[3:]
		} else {
			d.pending = b[2:]
		}
	}
	return events
}

func pollInput(fd int, decoder *inputDecoder, timeout int) ([]inputEvent, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	_, err := unix.Poll(fds, timeout)
	if errors.Is(err, unix.EINTR) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
		return nil, io.EOF
	}
	if fds[0].Revents&unix.POLLIN == 0 {
		return nil, nil
	}
	buf := make([]byte, 4096)
	n, err := unix.Read(fd, buf)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, io.EOF
	}
	return decoder.feed(buf[:n]), nil
}
func parseColorReply(s string) (int, color.RGBA, bool) {
	parts := strings.Split(s, ";")
	code, raw := 0, ""
	if len(parts) == 2 && (parts[0] == "10" || parts[0] == "11") {
		code, _ = strconv.Atoi(parts[0])
		raw = parts[1]
	} else if len(parts) == 3 && parts[0] == "4" && parts[1] == "4" {
		code, raw = 4, parts[2]
	} else {
		return 0, color.RGBA{}, false
	}
	if !strings.HasPrefix(raw, "rgb:") {
		return 0, color.RGBA{}, false
	}
	value := strings.TrimPrefix(raw, "rgb:")
	rgb := strings.Split(value, "/")
	if len(rgb) != 3 {
		return 0, color.RGBA{}, false
	}
	var components [3]uint8
	for i, h := range rgb {
		if len(h) < 1 || len(h) > 4 {
			return 0, color.RGBA{}, false
		}
		n, err := strconv.ParseUint(h, 16, 16)
		if err != nil {
			return 0, color.RGBA{}, false
		}
		maxValue := (uint64(1) << (4 * len(h))) - 1
		components[i] = uint8(n * 255 / maxValue)
	}
	return code, color.RGBA{components[0], components[1], components[2], 255}, true
}

func transmitViewport(w io.Writer, img image.Image, id uint32, cols, rows int) error {
	var pngData bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&pngData, img); err != nil {
		return err
	}
	payload := base64.StdEncoding.EncodeToString(pngData.Bytes())
	// Transmit without placement, then place atomically before deleting old ID.
	for first := true; len(payload) > 0; first = false {
		n := min(4096, len(payload))
		more := 0
		if n < len(payload) {
			more = 1
		}
		header := fmt.Sprintf("m=%d", more)
		if first {
			header = fmt.Sprintf("a=t,f=100,t=d,i=%d,q=2,m=%d", id, more)
		}
		if _, err := fmt.Fprintf(w, "\x1b_G%s;%s\x1b\\", header, payload[:n]); err != nil {
			return err
		}
		payload = payload[n:]
	}
	_, err := fmt.Fprintf(w, "\x1b[2;1H\x1b_Ga=p,i=%d,p=1,c=%d,r=%d,C=1,q=2\x1b\\", id, cols, rows)
	return err
}
func deleteImage(w io.Writer, id uint32) {
	if id != 0 {
		fmt.Fprintf(w, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
	}
}
func terminalLine(s string, cols int) string {
	var b strings.Builder
	used := 0
	for _, r := range safeText(s) {
		if r == '\n' || r == '\t' {
			r = ' '
		}
		w := 1
		if r > 255 {
			w = 2
		}
		if used+w > max(0, cols-1) {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String()
}

func view(o options, initial []byte, fonts *fontSet) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("view --in-place は対話端末で実行してください")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	var imageID uint32
	defer func() {
		deleteImage(os.Stdout, imageID)
		fmt.Fprint(os.Stdout, "\x1b[?1000l\x1b[?1006l\x1b[?25h\x1b[0m\x1b[?1049l")
		term.Restore(fd, old)
	}()
	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h\x1b[2J")
	fmt.Fprintf(os.Stdout, "\x1b_Ga=q,i=%d,s=1,v=1,f=24;AAAA\x1b\\", graphicsQueryID)
	if o.theme == "terminal" {
		fmt.Fprint(os.Stdout, terminalColorQuery)
	}
	decoder := &inputDecoder{}
	colors := paletteFor(o.theme)
	fg, bg, accent := colors.fg, colors.bg, colors.accent
	gotFG, gotBG, supported := false, false, false
	gotAccent := false
	handleColor := func(value string) bool {
		code, c, ok := parseColorReply(value)
		if !ok || o.theme != "terminal" {
			return false
		}
		switch code {
		case 10:
			fg, gotFG = c, true
		case 11:
			bg, gotBG = c, true
		case 4:
			accent, gotAccent = c, true
		}
		if gotFG && gotBG {
			next := makePalette(bg, fg)
			if gotAccent {
				next = next.withAccent(accent)
			}
			changed := next != colors
			colors = next
			return changed
		}
		return false
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		events, err := pollInput(fd, decoder, 50)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.kind == "quit" {
				return nil
			}
			if e.kind == "color" {
				handleColor(e.value)
			}
			if e.kind == "graphics" && strings.Contains(e.value, fmt.Sprintf("i=%d", graphicsQueryID)) && strings.HasSuffix(e.value, ";OK") {
				supported = true
			}
		}
		if supported && (o.theme != "terminal" || (gotFG && gotBG)) {
			break
		}
	}
	if !supported {
		return fmt.Errorf("Kitty Graphics Protocol の対応を確認できませんでした。macOS の Rex で実行してください")
	}
	size, err := getSize(fd)
	if err != nil {
		return err
	}
	scale := func(s terminalSize) float64 { return max(0.5, float64(s.height)/float64(s.rows)/20) }
	doc, err := layoutMarkdown(initial, o.file, size.width, scale(size), fonts, colors)
	if err != nil {
		return err
	}
	monitor := startMonitor(ctx, o.file, initial)
	data := initial
	offset := 0
	status := "表示中"
	dirty := true
	needLayout := false
	nextTheme := time.Now().Add(2 * time.Second)
	ready := false
	var currentVersion uint64
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		select {
		case ev, ok := <-monitor.updates:
			if !ok {
				return nil
			}
			if ev.version < currentVersion {
				break
			}
			currentVersion = ev.version
			if ev.err != nil {
				status = "読み込みエラー: " + ev.err.Error()
				dirty = true
			} else {
				data = ev.data
				needLayout = true
				label := "更新 "
				if ev.forced {
					label = "再読み込み "
				}
				status = label + time.Now().Format("15:04:05.000")
			}
		default:
		}
		newSize, e := getSize(fd)
		if e == nil && newSize != size {
			size = newSize
			needLayout = true
			dirty = true
		}
		if o.theme == "terminal" && time.Now().After(nextTheme) {
			fmt.Fprint(os.Stdout, terminalColorQuery)
			nextTheme = time.Now().Add(2 * time.Second)
		}
		if needLayout {
			candidate, e := layoutMarkdown(data, o.file, size.width, scale(size), fonts, colors)
			if e != nil {
				status = "描画エラー: " + e.Error()
				dirty = true
			} else {
				// Layout and painting are serialized in this loop, and snapshots
				// have increasing versions. No older render can replace a newer
				// display; continuous writes do not starve rendering either.
				doc = candidate
				dirty = true
			}
			needLayout = false
		}
		bodyHeight := (size.rows - 2) * size.height / size.rows
		offset = max(0, min(offset, max(0, doc.height-bodyHeight)))
		if dirty {
			img := doc.paint(offset, bodyHeight)
			previous := imageID
			imageID++
			if imageID == 0 || imageID == graphicsQueryID {
				imageID++
			}
			if err = transmitViewport(os.Stdout, img, imageID, size.cols, size.rows-2); err != nil {
				return err
			}
			deleteImage(os.Stdout, previous)
			fmt.Fprintf(os.Stdout, "\x1b[1;1H\x1b[2K%s\x1b[%d;1H\x1b[2K%s", terminalLine(filepathBase(o.file), size.cols), size.rows, terminalLine(fmt.Sprintf("%s  %d/%d  q:終了 r:再読込", status, offset, max(0, doc.height-bodyHeight)), size.cols))
			dirty = false
			if !ready {
				if err = reportReady(); err != nil {
					return err
				}
				ready = true
			}
		}
		events, err := pollInput(fd, decoder, 40)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, ev := range events {
			switch ev.kind {
			case "quit":
				return nil
			case "color":
				if handleColor(ev.value) {
					needLayout = true
				}
			case "key":
				step := max(1, int(24*scale(size)))
				switch ev.value {
				case "j":
					offset += step
					dirty = true
				case "k":
					offset -= step
					dirty = true
				case " ":
					offset += bodyHeight
					dirty = true
				case "b":
					offset -= bodyHeight
					dirty = true
				case "g":
					offset = 0
					dirty = true
				case "G":
					offset = max(0, doc.height-bodyHeight)
					dirty = true
				case "r":
					monitor.reload()
				}
			}
		}
	}
}
func filepathBase(path string) string {
	// filepath.Base is deliberately only used for a display label; identity
	// and file reads always retain the normalized absolute path.
	idx := strings.LastIndexByte(path, '/')
	return safeText(path[idx+1:])
}
