package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"reflect"
	"strings"
	"testing"
)

func TestInputDecoderFragmentation(t *testing.T) {
	stream := fmt.Sprintf("\x1b_Gi=%d;OK\x1b\\\x1b]10;rgb:ffff/0000/8888\x07\x1b[A\x1b[6~\x1b[<64;10;10M\x1b[<65;10;10Mq", graphicsQueryID)
	var all []inputEvent
	d := inputDecoder{}
	for _, b := range []byte(stream) {
		all = append(all, d.feed([]byte{b})...)
	}
	want := []inputEvent{{"graphics", fmt.Sprintf("i=%d;OK", graphicsQueryID)}, {"color", "10;rgb:ffff/0000/8888"}, {"key", "k"}, {"key", " "}, {"key", "k"}, {"key", "j"}, {"quit", ""}}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("%+v", all)
	}
}
func TestRepliesCannotBecomeKeys(t *testing.T) {
	d := inputDecoder{}
	got := d.feed([]byte("\x1b_Gi=1;query-rejected qjjr\x1b\\\x1b]0;qjkr\x07"))
	if len(got) != 2 || got[0].kind != "graphics" || got[1].kind != "color" {
		t.Fatalf("%+v", got)
	}
}
func TestParseColor(t *testing.T) {
	code, c, ok := parseColorReply("11;rgb:ffff/8080/0000")
	if !ok || code != 11 || c != (color.RGBA{255, 128, 0, 255}) {
		t.Fatalf("%d %v %t", code, c, ok)
	}
	for _, s := range []string{"10;red", "11;rgb:x/00/00", "4;rgb:ff/ff/ff", "10;rgb:fffff/00/00", "11;rgb:/00/00"} {
		if _, _, ok := parseColorReply(s); ok {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestTerminalLineSafe(t *testing.T) {
	got := terminalLine("日本語\x1b[31m\nhello", 9)
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\n") {
		t.Fatalf("unsafe: %q", got)
	}
	if got == "" {
		t.Fatal("no text")
	}
}
func TestTransmitViewport(t *testing.T) {
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	if err := transmitViewport(&out, img, 12, 5, 3); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "a=t,f=100,t=d,i=12,q=2") || !strings.Contains(s, "a=p,i=12,p=1,c=5,r=3,C=1,q=2") {
		t.Fatal("missing transmission/placement")
	}
	if strings.Contains(s, "a=d") {
		t.Fatal("deleted before placed")
	}
	deleteImage(&out, 12)
	if !strings.HasSuffix(out.String(), "\x1b_Ga=d,d=I,i=12,q=2\x1b\\") {
		t.Fatal("wrong image deletion")
	}
}
