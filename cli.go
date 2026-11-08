package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

type usageError struct{ message string }

func (e *usageError) Error() string           { return e.message }
func usagef(format string, args ...any) error { return &usageError{fmt.Sprintf(format, args...)} }

type options struct {
	file, output, theme, font, bold string
	inPlace, below                  bool
	ratio, width                    int
}

const help = `rex-md-viewer: Markdown を Rex でプレビューする

Usage:
  rex-md-viewer view [--in-place | --below] [--ratio 50] [OPTIONS] FILE
  rex-md-viewer hide FILE
  rex-md-viewer render [--width 1000] [OPTIONS] -o OUTPUT.png FILE
  rex-md-viewer skill install --dir DIR [--force]
  rex-md-viewer skill show
  rex-md-viewer --version

描画オプション:
  --theme terminal|light|dark   view の既定: terminal、render の既定: light
  --font PATH                   本文用 TTF/OTF/TTC（REX_MD_VIEWER_FONT より優先）
  --font-bold PATH              太字用フォント（REX_MD_VIEWER_FONT_BOLD より優先）

フォントは環境変数 → SF Pro Text → Inter → 同梱 Go フォントの順に選びます。
不足する日本語のグリフはシステム上の日本語フォントで補います。

view は右側に分割して表示し、--in-place は現在の端末に表示します。
--in-place と --below / --ratio は併用できません。
q / Ctrl-C: 終了、j/k: スクロール、Space/b: ページ移動、g/G: 先頭/末尾、r: 再読み込み
`

func parseOptions(command string, args []string, out io.Writer) (options, error) {
	o := options{ratio: 50, width: 1000, theme: "terminal"}
	if command == "render" {
		o.theme = "light"
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.Usage = func() { fmt.Fprint(out, help) }
	f.StringVar(&o.theme, "theme", o.theme, "配色")
	f.StringVar(&o.font, "font", "", "本文フォント")
	f.StringVar(&o.bold, "font-bold", "", "太字フォント")
	if command == "view" {
		f.BoolVar(&o.inPlace, "in-place", false, "現在の端末で表示")
		f.BoolVar(&o.below, "below", false, "下側に分割")
		f.IntVar(&o.ratio, "ratio", o.ratio, "ペインの割合")
	} else {
		f.IntVar(&o.width, "width", 1000, "画像の幅")
		f.StringVar(&o.output, "o", "", "出力 PNG")
	}
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, flag.ErrHelp
		}
		return o, usagef("%s", err)
	}
	if f.NArg() != 1 {
		return o, usagef("%s: FILE を 1 つ指定してください", command)
	}
	o.file = f.Arg(0)
	if o.theme != "light" && o.theme != "dark" && !(command == "view" && o.theme == "terminal") {
		return o, usagef("不正な --theme: %s", o.theme)
	}
	if command == "view" {
		splitSet := false
		f.Visit(func(fl *flag.Flag) {
			if fl.Name == "below" || fl.Name == "ratio" {
				splitSet = true
			}
		})
		if o.inPlace && splitSet {
			return o, usagef("--in-place と --below / --ratio は併用できません")
		}
		if o.ratio < 1 || o.ratio > 99 {
			return o, usagef("--ratio は 1〜99 を指定してください")
		}
	} else {
		if o.width < 1 || o.width > 8192 {
			return o, usagef("--width は 1〜8192 を指定してください")
		}
		if o.output == "" {
			return o, usagef("render には -o OUTPUT.png が必要です")
		}
	}
	return o, nil
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, help)
		return usagef("コマンドを指定してください")
	}
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Fprint(out, help)
		return nil
	case "--version":
		fmt.Fprintln(out, version)
		return nil
	case "skill":
		return runSkill(args[1:], out)
	case "hide":
		f := flag.NewFlagSet("hide", flag.ContinueOnError)
		f.SetOutput(io.Discard)
		if err := f.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprint(out, help)
				return nil
			}
			return usagef("%s", err)
		}
		if f.NArg() != 1 {
			return usagef("hide: FILE を 1 つ指定してください")
		}
		path, err := filepath.Abs(f.Arg(0))
		if err != nil {
			return err
		}
		return managePane(path, options{}, true, out)
	case "view", "render":
		o, err := parseOptions(args[0], args[1:], out)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		if err != nil {
			return err
		}
		o.file, err = filepath.Abs(o.file)
		if err != nil {
			return err
		}
		// Resolve font paths before passing them to a new pane.
		for _, p := range []*string{&o.font, &o.bold} {
			if *p != "" {
				*p, err = filepath.Abs(*p)
				if err != nil {
					return err
				}
			}
		}
		data, err := readMarkdown(o.file)
		if err != nil {
			return err
		}
		fonts, err := loadFonts(o.font, o.bold)
		if err != nil {
			return err
		}
		defer fonts.close()
		if args[0] == "render" {
			return renderFile(o, data, fonts)
		}
		if o.inPlace {
			return view(o, data, fonts)
		}
		// Pin resolved preferences in the child, including fonts selected by
		// environment variables. Rex need not inherit the calling shell's environment.
		o.font, o.bold = fonts.bodyPath, fonts.boldPath
		return managePane(o.file, o, false, out)
	default:
		return usagef("不明なコマンド: %s", args[0])
	}
}

func childArgs(self string, o options) []string {
	args := []string{self, "view", "--in-place", "--theme", o.theme}
	if o.font != "" {
		args = append(args, "--font", o.font)
	}
	if o.bold != "" {
		args = append(args, "--font-bold", o.bold)
	}
	return append(args, "--", o.file)
}

func renderFile(o options, data []byte, fonts *fontSet) error {
	dest, err := filepath.Abs(o.output)
	if err != nil {
		return err
	}
	in, err := os.Stat(o.file)
	if err != nil {
		return err
	}
	if existing, e := os.Stat(dest); dest == o.file || (e == nil && os.SameFile(in, existing)) {
		return fmt.Errorf("入力 Markdown への上書きはできません")
	}
	doc, err := layoutMarkdown(data, o.file, o.width, 1, fonts, paletteFor(o.theme))
	if err != nil {
		return err
	}
	if int64(doc.width)*int64(doc.height) > maxRenderPixels {
		return fmt.Errorf("PNG が大きすぎます（上限 %s 画素）。--width を小さくするか文書を分割してください", strconv.Itoa(maxRenderPixels))
	}
	img := doc.paint(0, doc.height)
	return writePNG(dest, img)
}
