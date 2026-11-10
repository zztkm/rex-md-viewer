# rex md viewer

A tool for previewing Markdown files in Rex.

Inspired by: https://github.com/almonk/rexpad.git

## Installation

Building requires Go 1.27 or later.
Displaying previews requires a terminal that supports the Kitty Graphics Protocol. Split-pane previews use Rex and the `rex` CLI.
A font that supports Japanese is also required to display Japanese text.
The initial release has been tested in Rex on macOS.
No browser, web server, or Node.js is required.

```bash
go install github.com/zztkm/rex-md-viewer@latest
```

Add the `go install` destination (usually `~/go/bin`) to your `PATH`.

## Usage

```bash
# Open a preview on the right. Repeated calls reuse the same viewer
rex-md-viewer view README.md

# Display in the current terminal (press q or Ctrl-C to quit)
rex-md-viewer view --in-place README.md

# Close the split-pane preview
rex-md-viewer hide README.md

# Generate a PNG without connecting to Rex
rex-md-viewer render -o preview.png testdata/sample.md

# Install the Agent Skill
rex-md-viewer skill install --dir ~/.pi/agent/skills
```

### Font configuration

You can configure regular and bold fonts separately using environment variables.
The viewer does not inherit the font from Rex's terminal settings.

```bash
export REX_MD_VIEWER_FONT="/path/to/regular.ttf"
export REX_MD_VIEWER_FONT_BOLD="/path/to/bold.ttf"
rex-md-viewer view README.md
```

For both regular and bold text, fonts are selected in this order: CLI option → environment variable → SF Pro Text → Inter → bundled Go font.

| Purpose | CLI option | Environment variable | Final fallback |
| --- | --- | --- | --- |
| Body text | `--font PATH` | `REX_MD_VIEWER_FONT` | Go Regular |
| Headings and bold text | `--font-bold PATH` | `REX_MD_VIEWER_FONT_BOLD` | Go Bold |

TTF, OTF, and TTC files are supported. Specify a file path, not a font name.
The viewer looks for static SF Pro Text OTF files in `/Library/Fonts` and `~/Library/Fonts`, and for Inter in known Linux locations.
If a path set through an environment variable does not exist or cannot be loaded, the viewer tries the next candidate.
An invalid path explicitly provided through a CLI option, however, results in an error.
Relative paths are resolved against the caller's working directory, and the resolved settings are passed to the split-pane viewer.
To change the fonts of an existing viewer, close it with `hide` and reopen it.

Characters missing from the primary font are rendered using per-character fallbacks to Japanese fonts in known locations, such as Noto Sans JP, Hiragino on macOS, and Noto Sans CJK on Linux.
If neither the primary nor fallback fonts can render a kanji, hiragana, or katakana character, the viewer reports an error with font configuration instructions.
English documents can therefore be displayed using only the bundled Go fonts, but Japanese documents require a Japanese font.
Japanese fonts are not bundled. Only Go Regular, Go Bold, and Go Mono for ASCII characters in code are included.

Because the document is displayed as an image, the terminal's standard text selection, copy, and search features are unavailable.
Automatic updates watch the Markdown file itself. If you change only a local image, press `r` in the viewer to refresh.

## Development and testing

```bash
make build
make check test

# Check image transmission, updates, resizing, theme changes, and terminal restoration on exit in a pseudo-terminal
make smoke

# Run only inside Rex. Creates test panes and closes them on exit
make smoke-rex

# Test link interactions through Rex's input encoder (does not launch a browser)
make smoke-links-rex
```

Smoke tests require Python 3.
See [docs/testing.md](docs/testing.md) for test coverage and limitations.

## License

This project is licensed under the [MIT License](LICENSE).
Third-party code and bundled fonts are covered by their respective licenses.
See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for details.
When redistributing binaries, include both `LICENSE` and `THIRD_PARTY_NOTICES.md`.
