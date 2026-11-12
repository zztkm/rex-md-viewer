package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
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
	scale                           float64
}

const scaleEnv = "REX_MD_VIEWER_SCALE"

const help = `rex-md-viewer: Preview Markdown in Rex

Usage:
  rex-md-viewer view [--in-place | --below] [--ratio 50] [--scale 1] [OPTIONS] FILE
  rex-md-viewer hide FILE
  rex-md-viewer render [--width 1000] [--scale 1] [OPTIONS] -o OUTPUT.png FILE
  rex-md-viewer skill install --dir DIR [--force]
  rex-md-viewer skill show
  rex-md-viewer --version

Rendering options:
  --scale N                     Relative size, 0.5 to 4 (default: 1); overrides REX_MD_VIEWER_SCALE
  --theme terminal|light|dark   Default: terminal for view, light for render
  --font PATH                   Body font (TTF/OTF/TTC); overrides REX_MD_VIEWER_FONT
  --font-bold PATH              Bold font (TTF/OTF/TTC); overrides REX_MD_VIEWER_FONT_BOLD

Font priority: CLI option > environment variable > SF Pro Text > Inter > bundled Go font.
Missing Japanese glyphs fall back to Japanese fonts installed on the system.

view opens a split pane on the right; --in-place displays in the current terminal.
--in-place cannot be combined with --below or --ratio.
--scale multiplies the automatic view size; render keeps --width in pixels.
Existing panes keep their settings; hide and reopen to change the scale.
q / Ctrl-C: quit, j/k: scroll, Space/b: page down/up, g/G: top/bottom, r: reload
`

func parseOptions(command string, args []string, out io.Writer) (options, error) {
	o := options{ratio: 50, width: 1000, theme: "terminal"}
	if command == "render" {
		o.theme = "light"
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.Usage = func() { fmt.Fprint(out, help) }
	scaleValue := "1"
	f.StringVar(&scaleValue, "scale", scaleValue, "Relative size (0.5 to 4)")
	f.StringVar(&o.theme, "theme", o.theme, "Color theme")
	f.StringVar(&o.font, "font", "", "Body font")
	f.StringVar(&o.bold, "font-bold", "", "Bold font")
	if command == "view" {
		f.BoolVar(&o.inPlace, "in-place", false, "Display in the current terminal")
		f.BoolVar(&o.below, "below", false, "Split below")
		f.IntVar(&o.ratio, "ratio", o.ratio, "Pane size percentage")
	} else {
		f.IntVar(&o.width, "width", 1000, "Image width in pixels")
		f.StringVar(&o.output, "o", "", "Output PNG path")
	}
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, flag.ErrHelp
		}
		return o, usagef("%s", err)
	}
	if f.NArg() != 1 {
		return o, usagef("%s: specify exactly one FILE", command)
	}
	o.file = f.Arg(0)
	scaleSet := false
	f.Visit(func(fl *flag.Flag) {
		if fl.Name == "scale" {
			scaleSet = true
		}
	})
	scaleSource := "--scale"
	if !scaleSet {
		if value := os.Getenv(scaleEnv); value != "" {
			scaleValue, scaleSource = value, scaleEnv
		}
	}
	var err error
	o.scale, err = strconv.ParseFloat(scaleValue, 64)
	if err != nil || math.IsNaN(o.scale) || math.IsInf(o.scale, 0) || o.scale < 0.5 || o.scale > 4 {
		return o, usagef("%s must be a finite number between 0.5 and 4 (got %q)", scaleSource, scaleValue)
	}
	if o.theme != "light" && o.theme != "dark" && !(command == "view" && o.theme == "terminal") {
		return o, usagef("invalid --theme: %s", o.theme)
	}
	if command == "view" {
		splitSet := false
		f.Visit(func(fl *flag.Flag) {
			if fl.Name == "below" || fl.Name == "ratio" {
				splitSet = true
			}
		})
		if o.inPlace && splitSet {
			return o, usagef("--in-place cannot be combined with --below or --ratio")
		}
		if o.ratio < 1 || o.ratio > 99 {
			return o, usagef("--ratio must be between 1 and 99")
		}
	} else {
		if o.width < 1 || o.width > 8192 {
			return o, usagef("--width must be between 1 and 8192")
		}
		if o.output == "" {
			return o, usagef("render requires -o OUTPUT.png")
		}
	}
	return o, nil
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, help)
		return usagef("specify a command")
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
			return usagef("hide: specify exactly one FILE")
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
		return usagef("unknown command: %s", args[0])
	}
}

func childArgs(self string, o options) []string {
	args := []string{self, "view", "--in-place", "--theme", o.theme, "--scale", strconv.FormatFloat(o.scale, 'g', -1, 64)}
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
		return fmt.Errorf("cannot overwrite the input Markdown file")
	}
	doc, err := layoutMarkdown(data, o.file, o.width, o.scale, fonts, paletteFor(o.theme))
	if err != nil {
		return err
	}
	if int64(doc.width)*int64(doc.height) > maxRenderPixels {
		return fmt.Errorf("PNG is too large (limit: %s pixels); reduce --width or split the document", strconv.Itoa(maxRenderPixels))
	}
	img := doc.paint(0, doc.height)
	return writePNG(dest, img)
}
