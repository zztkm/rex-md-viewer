package main

import (
	"image"
	"strconv"
	"strings"
)

// SGR のマウス報告には Shift/Alt/Ctrl は含まれるが、macOS の Cmd に対応するビットはない。
// Cmd の状態は、Kitty Keyboard Protocol で修飾キー自体の押下と解放を受け取って追跡する。
const viewerInputEnable = "\x1b[?1002h\x1b[?1004h\x1b[?1006h\x1b[?1016h\x1b[>11u\x1b[?1016$p\x1b[?u"
const viewerInputDisable = "\x1b[<u\x1b[?1000l\x1b[?1002l\x1b[?1004l\x1b[?1006l\x1b[?1016l"

func decodeCSI(seq string) []inputEvent {
	keys := map[string]string{"A": "k", "B": "j", "H": "g", "F": "G", "1~": "g", "4~": "G", "7~": "g", "8~": "G", "5~": "b", "6~": " "}
	if key, ok := keys[seq]; ok {
		return []inputEvent{{kind: "key", value: key}}
	}
	switch seq {
	case "I", "O":
		return []inputEvent{{kind: "focus", value: seq}}
	case "?1016;1$y":
		return []inputEvent{{kind: "pixels", value: "yes"}}
	}
	if strings.HasPrefix(seq, "?") && strings.HasSuffix(seq, "u") {
		flags, err := strconv.Atoi(seq[1 : len(seq)-1])
		if err == nil && flags >= 0 {
			return []inputEvent{{kind: "keyboard", value: strconv.Itoa(flags)}}
		}
	}
	if mouse, ok := parseMouse(seq); ok {
		if !mouse.release {
			switch mouse.code &^ (4 | 8 | 16) {
			case 64:
				return []inputEvent{{kind: "key", value: "k"}}
			case 65:
				return []inputEvent{{kind: "key", value: "j"}}
			}
		}
		return []inputEvent{{kind: "mouse", value: seq}}
	}
	if len(seq) < 2 {
		return nil
	}
	final := seq[len(seq)-1]
	if final != 'u' && final != '~' && !strings.ContainsRune("ABHF", rune(final)) {
		return nil
	}
	fields := strings.Split(seq[:len(seq)-1], ";")
	if len(fields) > 3 {
		return nil
	}
	code, err := strconv.Atoi(strings.Split(fields[0], ":")[0])
	if err != nil || code < 0 {
		return nil
	}
	mods, action := 0, 1
	if len(fields) > 1 {
		m := strings.Split(fields[1], ":")
		if len(m) > 2 {
			return nil
		}
		value, err := strconv.Atoi(m[0])
		if err != nil || value < 1 || value > 256 {
			return nil
		}
		mods = value - 1
		if len(m) == 2 {
			action, err = strconv.Atoi(m[1])
			if err != nil || action < 1 || action > 3 {
				return nil
			}
		}
	}
	events := []inputEvent{{kind: "mods", value: strconv.Itoa(mods)}}
	if action == 3 {
		return events
	}
	if final == 'u' {
		if (code == 'q' && mods == 0) || (code == 'c' && mods == 4) || (code == 3 && mods == 0) {
			return append(events, inputEvent{kind: "quit"})
		}
		if code == 'g' && mods == 1 {
			return append(events, inputEvent{kind: "key", value: "G"})
		}
		if mods != 0 {
			return events
		}
		if strings.ContainsRune("jk bgGr", rune(code)) {
			return append(events, inputEvent{kind: "key", value: string(rune(code))})
		}
		// Kitty functional key codes for the existing navigation bindings.
		key := map[int]string{57352: "k", 57353: "j", 57356: "g", 57357: "G", 57354: "b", 57355: " "}[code]
		if key != "" {
			return append(events, inputEvent{kind: "key", value: key})
		}
	} else if mods == 0 {
		// Kitty encodes arrows with CSI 1;mods A and PageUp with CSI 5;mods ~.
		key := keys[string(final)]
		if final == '~' {
			key = keys[strconv.Itoa(code)+"~"]
		}
		if key != "" {
			return append(events, inputEvent{kind: "key", value: key})
		}
	}
	return events
}

type mouseReport struct {
	code, x, y int
	release    bool
}

func parseMouse(seq string) (mouseReport, bool) {
	if len(seq) < 2 || seq[0] != '<' || (seq[len(seq)-1] != 'M' && seq[len(seq)-1] != 'm') {
		return mouseReport{}, false
	}
	parts := strings.Split(seq[1:len(seq)-1], ";")
	if len(parts) != 3 {
		return mouseReport{}, false
	}
	var values [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || v > 1<<30 || (i > 0 && v == 0) {
			return mouseReport{}, false
		}
		values[i] = v
	}
	if values[0] > 255 {
		return mouseReport{}, false
	}
	return mouseReport{values[0], values[1], values[2], seq[len(seq)-1] == 'm'}, true
}

// Never interpret cell coordinates as pixels when capability negotiation failed.
func pointerInDocument(mouse mouseReport, size terminalSize, doc *document, offset int) (image.Point, bool) {
	if size.cols < 1 || size.rows < 3 || size.width < 1 || size.height < size.rows || doc == nil {
		return image.Point{}, false
	}
	x, y := mouse.x-1, mouse.y-1
	top, bottom := size.height/size.rows, (size.rows-1)*size.height/size.rows
	if x < 0 || x >= size.width || y < top || y >= bottom {
		return image.Point{}, false
	}
	height := (size.rows - 2) * size.height / size.rows
	p := image.Pt(x*doc.width/size.width, (y-top)*height/(bottom-top)+offset)
	return p, p.In(image.Rect(0, 0, doc.width, doc.height))
}

type linkInput struct {
	pixels, keyboard bool
	mods             int
	pressed          int
	origin           image.Point
}

func (s *linkInput) cancel()       { s.pressed = 0 }
func (s *linkInput) capable() bool { return s.pixels && s.keyboard }

// observe is also used during startup, when replies and keys can be interleaved.
func (s *linkInput) observe(ev inputEvent) {
	switch ev.kind {
	case "pixels":
		s.pixels = ev.value == "yes"
	case "keyboard":
		flags, _ := strconv.Atoi(ev.value)
		s.keyboard = flags&11 == 11
	case "mods":
		s.mods, _ = strconv.Atoi(ev.value)
		if s.mods != 8 {
			s.cancel()
		}
	case "focus":
		// macOS 版 Rex の実操作では、左ボタンの押下と解放の間に、フォーカスを
		// 失っていなくても focus-in (CSI I) が再送されることがある。
		// ここで Cmd の状態と押下記録を消すと、解放時にクリックが成立しなくなる。
		// focus-in は状態の通知であり、新たなフォーカス移動とみなしてはいけない。
		// 状態の消去は focus-out (CSI O) のみで行い、再取得時にも復元しない。
		if ev.value == "O" {
			s.mods = 0
			s.cancel()
		}
	}
}

// Open only after a matching press/release. Motion, other buttons, focus loss
// and any repaint cancel the gesture so a stale target cannot be activated.
func (s *linkInput) click(ev inputEvent, size terminalSize, doc *document, offset int) string {
	s.observe(ev)
	if ev.kind != "mouse" {
		return ""
	}
	mouse, ok := parseMouse(ev.value)
	if !ok || !s.capable() || s.mods != 8 || mouse.code != 0 {
		s.cancel()
		return ""
	}
	p, inside := pointerInDocument(mouse, size, doc, offset)
	if !inside {
		s.cancel()
		return ""
	}
	target := doc.linkAt(p)
	if !mouse.release {
		s.pressed, s.origin = target, p
		return ""
	}
	pressed := s.pressed
	s.cancel()
	if pressed != 0 && pressed == target && p == s.origin && target <= len(doc.linkTargets) {
		return doc.linkTargets[target-1]
	}
	return ""
}
