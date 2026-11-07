package main

import (
	"image/color"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Bound the extra work of syntax analysis independently of the document limit.
// Larger blocks still render in full, but without highlighting.
const maxHighlightBytes = 256 << 10

// highlightCode changes only the ink of already-laid-out glyphs. Token
// boundaries must not change word wrapping, whitespace, or font metrics.
func highlightCode(ops []paintOp, source, language string, colors palette) {
	if language == "" || source == "" || len(source) > maxHighlightBytes {
		return
	}
	complete := false
	defer func() {
		// Chroma iterators can panic on lexer errors. Leave the original,
		// literal code visible rather than failing the document or retaining
		// a partially highlighted block.
		if recover() != nil || !complete {
			for i := range ops {
				ops[i].style.ink = colors.fg
			}
		}
	}()
	lexer := lexers.Get(language)
	if lexer == nil {
		return
	}
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return
	}
	style := styles.Get("github")
	if luminance(colors.panel) < 0.18 {
		style = styles.Get("github-dark")
	}
	base := style.Get(chroma.Text).Colour
	remaining, index := source, 0
	for token := iterator(); token != chroma.EOF; token = iterator() {
		// Reject lexers that alter the input instead of risking a colour
		// offset. The original text remains the sole source of glyphs.
		if !strings.HasPrefix(remaining, token.Value) {
			return
		}
		remaining = remaining[len(token.Value):]
		ink := colors.fg
		// Error colours may rely on a contrasting token background, which
		// we intentionally do not draw. Keep errors and whitespace in the
		// document foreground instead.
		if c := style.Get(token.Type).Colour; c.IsSet() && c != base && token.Type != chroma.Error && token.Type != chroma.TextWhitespace {
			ink = color.RGBA{c.Red(), c.Green(), c.Blue(), 255}
		}
		for _, r := range safeText(token.Value) {
			if r == '\n' {
				continue
			}
			count := 1
			if r == '\t' {
				count = 4 // Match inlineWithLineHeight's literal tab expansion.
			}
			for range count {
				if index >= len(ops) {
					return
				}
				ops[index].style.ink = ink
				index++
			}
		}
	}
	complete = remaining == "" && index == len(ops)
}
