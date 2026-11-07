package main

import (
	"fmt"
	"image"
	"reflect"
	"testing"
)

func TestExtendedInputFragmentation(t *testing.T) {
	stream := "\x1b[?1016;1$y\x1b[?11u\x1b[I\x1b[57444;9u\x1b[<0;50;40M\x1b[<0;50;40m\x1b[57444;1:3u\x1b[O\x1b[103;2u\x1b[113;1:3u\x1b[113u"
	want := []inputEvent{{"pixels", "yes"}, {"keyboard", "11"}, {"focus", "I"}, {"mods", "8"},
		{"mouse", "<0;50;40M"}, {"mouse", "<0;50;40m"}, {"mods", "0"}, {"focus", "O"},
		{"mods", "1"}, {"key", "G"}, {"mods", "0"}, {"mods", "0"}, {"quit", ""}}
	for chunk := 1; chunk <= len(stream); chunk++ {
		var events []inputEvent
		var decoder inputDecoder
		for start := 0; start < len(stream); start += chunk {
			events = append(events, decoder.feed([]byte(stream[start:min(len(stream), start+chunk)]))...)
		}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("chunk=%d: %+v", chunk, events)
		}
	}
}

func TestKittyExistingControls(t *testing.T) {
	cases := map[string]string{
		"106u": "j", "107u": "k", "32u": " ", "98u": "b", "103u": "g", "103;2u": "G", "71u": "G", "114u": "r",
		"1;1A": "k", "1;1B": "j", "1;1H": "g", "1;1F": "G", "5;1~": "b", "6;1~": " ",
		"57352u": "k", "57353u": "j", "57356u": "g", "57357u": "G", "57354u": "b", "57355u": " ",
		"106;1:2u": "j",
	}
	for seq, want := range cases {
		events := decodeCSI(seq)
		last := events[len(events)-1]
		if last != (inputEvent{"key", want}) {
			t.Errorf("%q: %+v, want %q", seq, events, want)
		}
	}
	for _, seq := range []string{"113u", "99;5u", "3u"} {
		events := decodeCSI(seq)
		if events[len(events)-1].kind != "quit" {
			t.Errorf("quit not recognized: %q: %v", seq, events)
		}
	}
	for _, seq := range []string{"113;9u", "113;1:3u", "106;5u", "57444;9u", "1;9A", "114;3u"} {
		for _, ev := range decodeCSI(seq) {
			if ev.kind == "quit" || ev.kind == "key" {
				t.Errorf("modified/released key acted as a normal key: %q: %v", seq, ev)
			}
		}
	}
}

func TestInvalidMouseAndKeyboardReports(t *testing.T) {
	for _, seq := range []string{"<x;1;1M", "<0;x;1M", "<0;1;1;1M", "<0;0;1M", "<0;1;-1M", "<0;1;999999999999999999999999M", "<256;1;1M", "<0;1;1X"} {
		if _, ok := parseMouse(seq); ok {
			t.Errorf("accepted %q", seq)
		}
		if len(decodeCSI(seq)) != 0 {
			t.Errorf("invalid mouse report became input: %q", seq)
		}
	}
	for _, seq := range []string{"113;0u", "113;-1u", "113;999999999999999999999u", "113;1:4u", "113;1:xu", "113;1:1:1u", "x;1u", "?bad-u"} {
		if got := decodeCSI(seq); len(got) != 0 {
			t.Errorf("invalid keyboard report became input: %q: %v", seq, got)
		}
	}
	for _, seq := range []string{"<64;1;1M", "<68;1;1M", "<80;1;1M"} {
		if got := decodeCSI(seq); !reflect.DeepEqual(got, []inputEvent{{"key", "k"}}) {
			t.Errorf("wheel: %q: %v", seq, got)
		}
	}
}

func linkInputFixture() (*document, terminalSize, linkInput) {
	doc := &document{width: 640, height: 1000, linkTargets: []string{"https://example.com/a", "https://example.com/b"},
		links: []linkRegion{{image.Rect(20, 10, 25, 30), 1}, {image.Rect(25, 10, 30, 30), 2}}}
	return doc, terminalSize{80, 24, 640, 432}, linkInput{pixels: true, keyboard: true, mods: 8}
}

func mouseInput(x, y int, release bool) inputEvent {
	final := "M"
	if release {
		final = "m"
	}
	return inputEvent{"mouse", fmt.Sprintf("<0;%d;%d%s", x+1, y+19, final)} // body starts at pixel 18
}

func TestPixelClickSelectsOnlyItsLink(t *testing.T) {
	doc, size, state := linkInputFixture()
	for _, item := range []struct {
		x    int
		want string
	}{{19, ""}, {20, doc.linkTargets[0]}, {24, doc.linkTargets[0]}, {25, doc.linkTargets[1]}, {29, doc.linkTargets[1]}, {30, ""}} {
		if got := state.click(mouseInput(item.x, 15, false), size, doc, 0); got != "" {
			t.Fatal("press opened browser")
		}
		if got := state.click(mouseInput(item.x, 15, true), size, doc, 0); got != item.want {
			t.Errorf("x=%d got %q want %q", item.x, got, item.want)
		}
		if got := state.click(mouseInput(item.x, 15, true), size, doc, 0); got != "" {
			t.Fatal("duplicate release opened browser")
		}
	}
}

// macOS 版 Rex で実際に Cmd＋クリックしたときの入力列を再生する回帰テスト。
// フォーカスを失っていなくても、マウス解放の直前に focus-in が再送される。
// API から Cmd とマウスのイベントだけを送るテストでは、この入力順序を見落とした。
// 修飾キーやマウス報告が有効という端末の応答だけでは、実操作の成功は保証できない。
func TestRexRepeatedFocusInBeforeMouseRelease(t *testing.T) {
	stream := "\x1b[I\x1b[I\x1b[<0;295;908M\x1b[I\x1b[<0;295;908m" +
		"\x1b[57444;9u\x1b[57444;1:3u" +
		"\x1b[57444;9u\x1b[<0;49;168M\x1b[I\x1b[<0;49;168m\x1b[57444;1:3u"
	for _, chunk := range []int{1, 3, len(stream)} {
		doc := &document{width: 496, height: 1000, linkTargets: []string{"https://example.com/a"},
			links: []linkRegion{{image.Rect(22, 140, 100, 164), 1}}}
		size := terminalSize{62, 78, 496, 1248}
		state := linkInput{pixels: true, keyboard: true}
		var decoder inputDecoder
		var opened []string
		for start := 0; start < len(stream); start += chunk {
			for _, ev := range decoder.feed([]byte(stream[start:min(len(stream), start+chunk)])) {
				if target := state.click(ev, size, doc, 0); target != "" {
					opened = append(opened, target)
				}
			}
		}
		if !reflect.DeepEqual(opened, doc.linkTargets) {
			t.Fatalf("chunk=%d: physical input trace opened %q, want %q", chunk, opened, doc.linkTargets)
		}
		if state.mods != 0 || state.pressed != 0 {
			t.Fatal("Cmd release left stale input state")
		}
	}
}

func TestClickCancellation(t *testing.T) {
	cases := map[string]func(*linkInput, terminalSize, *document){
		"repaint":    func(s *linkInput, _ terminalSize, _ *document) { s.cancel() },
		"focus loss": func(s *linkInput, _ terminalSize, _ *document) { s.observe(inputEvent{"focus", "O"}) },
		"focus loss and regain": func(s *linkInput, _ terminalSize, _ *document) {
			s.observe(inputEvent{"focus", "O"})
			s.observe(inputEvent{"focus", "I"})
		},
		"Cmd release":      func(s *linkInput, _ terminalSize, _ *document) { s.observe(inputEvent{"mods", "0"}) },
		"another modifier": func(s *linkInput, _ terminalSize, _ *document) { s.observe(inputEvent{"mods", "9"}) },
		"drag": func(s *linkInput, size terminalSize, d *document) {
			s.click(inputEvent{"mouse", "<32;23;34M"}, size, d, 0)
		},
		"right button": func(s *linkInput, size terminalSize, d *document) {
			s.click(inputEvent{"mouse", "<2;23;34M"}, size, d, 0)
		},
		"header": func(s *linkInput, size terminalSize, d *document) {
			s.click(inputEvent{"mouse", "<0;23;1M"}, size, d, 0)
		},
		"footer": func(s *linkInput, size terminalSize, d *document) {
			s.click(inputEvent{"mouse", "<0;23;432M"}, size, d, 0)
		},
	}
	for name, cancel := range cases {
		t.Run(name, func(t *testing.T) {
			doc, size, state := linkInputFixture()
			state.click(mouseInput(22, 15, false), size, doc, 0)
			cancel(&state, size, doc)
			if got := state.click(mouseInput(22, 15, true), size, doc, 0); got != "" {
				t.Fatalf("cancelled click opened %q", got)
			}
		})
	}
	for _, releaseX := range []int{23, 26, 40} {
		doc, size, state := linkInputFixture()
		state.click(mouseInput(22, 15, false), size, doc, 0)
		if got := state.click(mouseInput(releaseX, 15, true), size, doc, 0); got != "" {
			t.Errorf("moved release opened %q", got)
		}
	}
}

func TestLinkCapabilitiesFailClosed(t *testing.T) {
	for _, setup := range []linkInput{{}, {pixels: true}, {keyboard: true}, {pixels: true, keyboard: true}} {
		doc, size, _ := linkInputFixture()
		setup.click(mouseInput(22, 15, false), size, doc, 0)
		if got := setup.click(mouseInput(22, 15, true), size, doc, 0); got != "" {
			t.Fatalf("unsupported/plain click opened %q", got)
		}
	}
	var state linkInput
	state.observe(inputEvent{"pixels", "yes"})
	state.observe(inputEvent{"keyboard", "3"})
	if state.capable() {
		t.Fatal("keyboard mode without standalone modifier reporting accepted")
	}
	state.observe(inputEvent{"keyboard", "11"})
	if !state.capable() {
		t.Fatal("supported modes not detected")
	}
}

func TestPointerCoordinates(t *testing.T) {
	doc, size, _ := linkInputFixture()
	for _, item := range []struct {
		x, y, offset int
		want         image.Point
		ok           bool
	}{{1, 19, 0, image.Pt(0, 0), true}, {640, 414, 100, image.Pt(639, 495), true},
		{1, 18, 0, image.Point{}, false}, {1, 415, 0, image.Point{}, false},
		{641, 20, 0, image.Point{}, false}, {0, 20, 0, image.Point{}, false}} {
		p, ok := pointerInDocument(mouseReport{x: item.x, y: item.y}, size, doc, item.offset)
		if ok != item.ok || (ok && p != item.want) {
			t.Errorf("%+v: got %v, %v", item, p, ok)
		}
	}
	// A retained layout may be scaled after a resize/render failure.
	doc.width = 320
	if p, ok := pointerInDocument(mouseReport{x: 641, y: 37}, terminalSize{160, 24, 1280, 864}, doc, 50); !ok || p != image.Pt(160, 50) {
		t.Fatalf("scaled retained layout: %v %v", p, ok)
	}
}
