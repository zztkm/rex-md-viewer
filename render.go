package main

import (
	"bytes"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	ext "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const maxRenderPixels = 32 << 20
const maxImagePixels = 16 << 20
const maxGlyphs = 1_000_000

// Layout retains drawing commands, not a full-height bitmap. A viewer only
// allocates a bitmap for the visible viewport, even for a long document.
type palette struct {
	bg, fg, muted, accent, panel, line         color.RGBA
	taskFrame, taskDone, taskAccent, taskCheck color.RGBA
	taskActiveBg                               color.RGBA
}

func paletteFor(theme string) palette {
	if theme == "light" {
		p := makePalette(color.RGBA{250, 250, 252, 255}, color.RGBA{32, 36, 44, 255})
		p.taskFrame, p.taskDone = color.RGBA{193, 195, 198, 255}, color.RGBA{150, 153, 158, 255}
		p.taskActiveBg = color.RGBA{232, 238, 249, 255}
		return p
	}
	p := makePalette(color.RGBA{25, 28, 35, 255}, color.RGBA{222, 226, 234, 255})
	p.taskFrame, p.taskDone = color.RGBA{96, 99, 105, 255}, color.RGBA{128, 131, 137, 255}
	p.taskActiveBg = color.RGBA{37, 49, 72, 255}
	return p
}
func mix(a, b color.RGBA, n int) color.RGBA {
	return color.RGBA{uint8((int(a.R)*(100-n) + int(b.R)*n) / 100), uint8((int(a.G)*(100-n) + int(b.G)*n) / 100), uint8((int(a.B)*(100-n) + int(b.B)*n) / 100), 255}
}
func makePalette(bg, fg color.RGBA) palette {
	accent := color.RGBA{49, 95, 189, 255}
	frameMix, doneMix := 74, 56
	if luminance(bg) < 0.18 {
		accent = color.RGBA{84, 136, 238, 255}
		frameMix, doneMix = 62, 48
	}
	p := palette{bg: bg, fg: fg, muted: mix(bg, fg, 60), panel: mix(bg, fg, 7), line: mix(bg, fg, 22), taskFrame: mix(fg, bg, frameMix), taskDone: mix(fg, bg, doneMix)}
	return p.withAccent(accent)
}

type textStyle struct {
	size                          int
	bold, code, underline, strike bool
	ink                           color.RGBA
	baseline, lineHeight          int
	link                          int // index into document.linkTargets; zero means no link
}
type paintOp struct {
	rect     image.Rectangle
	ink      color.RGBA
	text     string
	style    textStyle
	picture  image.Image
	checkbox *taskStatus
	radius   float64
}
type document struct {
	width, height int
	ops           []paintOp
	fonts         *fontSet
	colors        palette
	dots          []workingDots
	linkTargets   []string
	links         []linkRegion
}
type layout struct {
	doc          *document
	source       []byte
	baseDir      string
	y, size, gap int
	scale        float64
	images       map[string]image.Image
	imagePixels  int64
	err          error
}
type inlinePart struct {
	text     string
	style    textStyle
	image    *ast.Image
	checkbox *taskStatus
}

func safeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return '\uFFFD'
		}
		return r
	}, s)
}

func layoutMarkdown(source []byte, path string, width int, scale float64, fonts *fontSet, colors palette) (*document, error) {
	if width < 1 || width > 8192 {
		return nil, fmt.Errorf("render width must be between 1 and 8192 pixels")
	}
	// CommonMark treats CRLF and CR as line endings, including inside code.
	// Normalize only the rendering input; the monitor compares original bytes.
	source = bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
	source = bytes.ReplaceAll(source, []byte("\r"), []byte("\n"))
	l := &layout{doc: &document{width: width, fonts: fonts, colors: colors}, source: source, baseDir: filepath.Dir(path), scale: scale, size: max(1, int(16*scale)), gap: max(1, int(10*scale)), images: make(map[string]image.Image)}
	pad := min(max(1, int(28*scale)), width/12)
	l.y = pad
	markdown := goldmark.New(goldmark.WithExtensions(extension.Table, extension.TaskList, extension.Strikethrough, extension.Linkify), goldmark.WithParserOptions(parser.WithInlineParsers(util.Prioritized(&wipParser{}, 0))))
	root := markdown.Parser().Parse(text.NewReader(source))
	if len(source) == 0 {
		l.inline([]inlinePart{{text: "Empty file", style: l.normal()}}, pad, width-pad, 0)
	} else {
		l.blocks(root, pad, width-pad)
	}
	if l.err != nil {
		return nil, l.err
	}
	l.doc.height = max(1, l.y+pad)
	l.doc.collectLinks()
	return l.doc, nil
}
func (l *layout) normal() textStyle { return textStyle{size: l.size, ink: l.doc.colors.fg} }
func (l *layout) addOp(op paintOp) bool {
	if l.err != nil {
		return false
	}
	if len(l.doc.ops) >= maxGlyphs {
		l.err = fmt.Errorf("document has too many rendering elements (limit: %d)", maxGlyphs)
		return false
	}
	l.doc.ops = append(l.doc.ops, op)
	return true
}
func (l *layout) fill(r image.Rectangle, c color.RGBA) bool {
	return l.addOp(paintOp{rect: r, ink: c})
}
func (l *layout) glyph(s string, x, y int, st textStyle, width int) {
	r := []rune(s)[0]
	if _, ok := l.doc.fonts.sourceFor(r, st.bold, st.code && r < 128); !ok && isJapanese(r) {
		l.err = fmt.Errorf("cannot render Japanese character %q (U+%04X); specify a Japanese font with --font or %s", r, r, fontEnv)
		return
	}
	height := st.size * 2
	if st.baseline > 0 {
		height = max(height, st.baseline+max(3, st.size))
		if st.code && st.lineHeight > 0 {
			height = max(height, max(0, st.baseline-st.size)+st.lineHeight)
		}
	}
	l.addOp(paintOp{rect: image.Rect(x, y, x+width, y+height), text: s, style: st})
}
func (l *layout) face(st textStyle, r rune) font.Face {
	return l.doc.fonts.runeFace(st.size, st.bold, st.code && r < 128, r)
}
func (l *layout) charWidth(r rune, st textStyle) int {
	advance, ok := l.face(st, r).GlyphAdvance(r)
	if !ok {
		return max(1, st.size)
	}
	return max(0, advance.Ceil())
}

func decoded(s []byte) string { return html.UnescapeString(string(util.UnescapePunctuations(s))) }
func (l *layout) inlineParts(parent ast.Node, st textStyle) []inlinePart {
	var parts []inlinePart
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		switch v := n.(type) {
		case *ast.Text:
			s := string(v.Segment.Value(l.source))
			if !v.IsRaw() {
				s = decoded([]byte(s))
			}
			parts = append(parts, inlinePart{text: s, style: st})
			if v.HardLineBreak() {
				parts = append(parts, inlinePart{text: "\n", style: st})
			} else if v.SoftLineBreak() {
				parts = append(parts, inlinePart{text: " ", style: st})
			}
		case *ast.String:
			parts = append(parts, inlinePart{text: string(v.Value), style: st})
		case *ast.Emphasis:
			next := st
			if v.Level == 2 {
				next.bold = true
			} else {
				next.underline = true
			}
			parts = append(parts, l.inlineParts(n, next)...)
		case *ext.Strikethrough:
			next := st
			next.strike = true
			parts = append(parts, l.inlineParts(n, next)...)
		case *ast.CodeSpan:
			next := st
			next.code = true
			parts = append(parts, l.inlineParts(n, next)...)
		case *ast.Link:
			next := l.linkStyle(st, decoded(v.Destination))
			parts = append(parts, l.inlineParts(n, next)...)
		case *ast.AutoLink:
			next := l.linkStyle(st, string(v.URL(l.source)))
			parts = append(parts, inlinePart{text: string(v.Label(l.source)), style: next})
		case *ast.Image:
			parts = append(parts, inlinePart{image: v, style: st})
		case *ext.TaskCheckBox:
			state := taskPending
			if v.IsChecked {
				state = taskDone
			}
			parts = append(parts, inlinePart{checkbox: &state, style: st})
		case *wipMarker:
			state := taskActive
			parts = append(parts, inlinePart{checkbox: &state, style: st})
		case *ast.RawHTML:
			parts = append(parts, inlinePart{text: string(v.Segments.Value(l.source)), style: st})
		default:
			parts = append(parts, l.inlineParts(n, st)...)
		}
	}
	return parts
}

// Words stay together when they fit. CJK characters remain individual break
// opportunities. Very long words and code lines wrap by rune, without loss.
func splitWords(s string) []string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range safeText(s) {
		if r < 128 && !unicode.IsSpace(r) {
			word.WriteRune(r)
		} else {
			flush()
			words = append(words, string(r))
		}
	}
	flush()
	return words
}

func (l *layout) inline(parts []inlinePart, left, right int, alignment ext.Alignment) {
	l.inlineWithLineHeight(parts, left, right, alignment, 0)
}
func (l *layout) inlineWithLineHeight(parts []inlinePart, left, right int, alignment ext.Alignment, fixedHeight int) (int, int) {
	left = min(left, l.doc.width-1)
	right = max(left+1, min(right, l.doc.width))
	x, lineTop := left, l.y
	baseHeight := max(1, l.size*3/2)
	if fixedHeight > 0 {
		baseHeight = fixedHeight
	}
	lineHeight := baseHeight
	lineStart := len(l.doc.ops)
	alignLine := func() {
		shift := 0
		if alignment == ext.AlignCenter {
			shift = (right - x) / 2
		} else if alignment == ext.AlignRight {
			shift = right - x
		}
		if shift > 0 {
			for i := lineStart; i < len(l.doc.ops); i++ {
				l.doc.ops[i].rect = l.doc.ops[i].rect.Add(image.Pt(shift, 0))
			}
		}
	}
	newLine := func() {
		alignLine()
		l.y = lineTop + lineHeight
		lineTop = l.y
		x = left
		lineHeight = baseHeight
		lineStart = len(l.doc.ops)
	}
	for _, p := range parts {
		st := p.style
		st.size = min(st.size, max(1, right-left))
		if p.image != nil {
			if x != left {
				newLine()
			}
			l.y = lineTop
			l.imageBlock(p.image, left, right, st)
			lineTop = l.y
			x = left
			lineStart = len(l.doc.ops)
			continue
		}
		// Task markers are rendered separately in the list's marker column.
		if p.checkbox != nil {
			continue
		}
		for _, word := range splitWords(p.text) {
			if word == "\n" {
				newLine()
				continue
			}
			if word == "\t" {
				word = "    "
			}
			w := 0
			for _, r := range word {
				w += l.charWidth(r, st)
			}
			if x > left && x+w > right && w <= right-left {
				newLine()
			}
			for _, r := range word {
				cw := l.charWidth(r, st)
				if x+cw > right && x > left {
					newLine()
				}
				// Normal prose does not carry a separator space onto the next
				// line. Code keeps its literal leading whitespace.
				if !st.code && r == ' ' && x == left {
					continue
				}
				if fixedHeight == 0 {
					lineHeight = max(lineHeight, st.size*3/2)
				} else {
					lineHeight = max(lineHeight, st.size)
				}
				l.glyph(string(r), x, lineTop, st, cw)
				x += cw
			}
		}
	}
	if x > left || l.y == lineTop {
		alignLine()
		l.y = lineTop + lineHeight
	}
	return x, lineTop
}

func (l *layout) blocks(parent ast.Node, left, right int) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		if l.err != nil {
			return
		}
		l.block(n, left, right)
	}
}
func (l *layout) block(n ast.Node, left, right int) {
	st := l.normal()
	switch v := n.(type) {
	case *ast.Heading:
		l.y += l.gap
		sizes := []float64{2, 1.65, 1.4, 1.2, 1.08, 1}
		st.size = max(1, int(float64(l.size)*sizes[v.Level-1]))
		st.bold = true
		l.inline(l.inlineParts(n, st), left, right, 0)
		l.y += l.gap
		if v.Level <= 2 {
			l.fill(image.Rect(left, l.y, right, l.y+1), l.doc.colors.line)
			l.y += l.gap
		}
	case *ast.Paragraph, *ast.TextBlock:
		l.inline(l.inlineParts(n, st), left, right, 0)
		if _, ok := n.(*ast.Paragraph); ok {
			l.y += l.gap
		}
	case *ast.Blockquote:
		top := l.y
		indent := min(l.size, right-left-1)
		l.blocks(n, left+max(0, indent), right)
		l.fill(image.Rect(left, top, left+max(1, l.size/6), l.y), l.doc.colors.line)
		l.y += l.gap
	case *ast.List:
		l.list(v, left, right)
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		var b strings.Builder
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			b.Write(seg.Value(l.source))
		}
		language := ""
		if fenced, ok := n.(*ast.FencedCodeBlock); ok {
			language = string(fenced.Language(l.source))
		}
		l.code(strings.TrimSuffix(b.String(), "\n"), language, left, right)
	case *ast.HTMLBlock:
		l.code(string(v.Text(l.source)), "", left, right)
	case *ast.ThematicBreak:
		l.y += l.gap
		l.fill(image.Rect(left, l.y, right, l.y+max(1, l.size/12)), l.doc.colors.line)
		l.y += l.gap * 2
	case *ext.Table:
		l.table(v, left, right)
	default:
		l.blocks(n, left, right)
	}
}
func (l *layout) listMarker(item ast.Node, ordered bool, number int, st textStyle) (string, taskStatus, int) {
	if state := itemTaskState(item); state != taskNone {
		m := taskLayout(l.taskScale() * float64(st.size) / float64(l.size))
		return "", state, m.box
	}
	label := "•"
	if ordered {
		label = fmt.Sprintf("%d.", number)
	}
	width := 0
	for _, r := range label {
		width += l.charWidth(r, st)
	}
	return label, taskNone, width
}

func (l *layout) list(list *ast.List, left, right int) {
	left = min(left, l.doc.width-1)
	right = max(left+1, min(right, l.doc.width))
	st := l.normal()
	taskOnly := list.FirstChild() != nil
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		_, check, _ := l.listMarker(item, list.IsOrdered(), 1, st)
		if check == taskNone {
			taskOnly = false
			break
		}
	}
	column, gap := 0, 0
	// Measure a shared marker column instead of reserving two em regardless
	// of marker width. Only shrink markers when the pane cannot fit them.
	for {
		column, gap = 0, max(1, st.size/2)
		if taskOnly {
			gap = taskLayout(l.taskScale() * float64(st.size) / float64(l.size)).gap
		}
		number := max(1, list.Start)
		for item := list.FirstChild(); item != nil; item = item.NextSibling() {
			_, _, width := l.listMarker(item, list.IsOrdered(), number, st)
			column = max(column, width)
			number++
		}
		if column+gap < right-left || st.size == 1 {
			break
		}
		st.size--
	}
	bodyLeft := left + column + gap
	stacked := bodyLeft >= right
	if stacked {
		bodyLeft = left
	}
	number := max(1, list.Start)
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		if l.err != nil {
			return
		}
		label, check, width := l.listMarker(item, list.IsOrdered(), number, st)
		activeRow := -1
		if check == taskActive {
			activeRow = l.startActiveRow(left, right)
		}
		if check != taskNone {
			y := l.y
			if !stacked {
				y += max(0, (taskLayout(l.taskScale()).pitch-width)/2)
			}
			l.drawCheckbox(left, y, width, check)
		} else {
			x := left
			if list.IsOrdered() {
				x += column - width
			}
			// Markers never use the wrapping inline layout. Multi-digit numbers
			// stay on the same row, right-aligned within the shared column.
			for _, r := range label {
				w := l.charWidth(r, st)
				l.glyph(string(r), x, l.y, st, w)
				x += w
			}
		}
		if stacked {
			if check != taskNone {
				l.y += width + gap
			} else {
				l.y += max(2, st.size*2)
			}
		}
		bodyTop := l.y
		var lastParagraph ast.Node
		for block := item.FirstChild(); block != nil; block = block.NextSibling() {
			switch block.(type) {
			case *ast.Paragraph, *ast.TextBlock:
				lastParagraph = block
			}
		}
		for block := item.FirstChild(); block != nil; block = block.NextSibling() {
			if l.err != nil {
				return
			}
			_, paragraph := block.(*ast.Paragraph)
			_, textBlock := block.(*ast.TextBlock)
			if check != taskNone && (paragraph || textBlock) {
				row := activeRow
				if check == taskActive && block != item.FirstChild() {
					row = l.startActiveRow(left, right)
				}
				l.taskParagraph(block, bodyLeft, right, check, block == item.FirstChild(), check == taskActive && block == lastParagraph)
				if check == taskActive {
					l.finishActiveRow(row)
				}
			} else {
				l.block(block, bodyLeft, right)
			}
		}
		if l.y == bodyTop {
			l.y += max(1, l.size*3/2)
		}
		number++
	}
	l.y += l.gap
}

func (l *layout) code(s, language string, left, right int) {
	top, index := l.y, len(l.doc.ops)
	if !l.fill(image.Rect(left, top, right, top), l.doc.colors.panel) {
		return
	}
	pad := min(l.gap, max(0, (right-left-1)/4))
	l.y += pad
	st := l.normal()
	st.code = true
	glyphStart := len(l.doc.ops)
	l.inline([]inlinePart{{text: s, style: st}}, left+pad, right-pad, 0)
	if l.err == nil {
		highlightCode(l.doc.ops[glyphStart:], s, language, l.doc.colors)
	}
	l.y += pad
	l.doc.ops[index].rect.Max.Y = l.y
	l.y += l.gap
}

func (l *layout) table(t *ext.Table, left, right int) {
	cols := len(t.Alignments)
	if cols == 0 {
		return
	}
	// Cells always stay inside their column; font size adapts to very narrow cells.
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		top := l.y
		bottom := top
		col := 0
		_, header := row.(*ext.TableHeader)
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			x1 := left + (right-left)*col/cols
			x2 := left + (right-left)*(col+1)/cols
			start := len(l.doc.ops)
			bg := l.doc.colors.bg
			if header {
				bg = l.doc.colors.panel
			}
			if !l.fill(image.Rect(x1, top, x2, top), bg) {
				return
			}
			pad := min(l.gap, max(0, (x2-x1-1)/4))
			l.y = top + pad
			st := l.normal()
			st.bold = header
			alignment := ext.AlignLeft
			if c, ok := cell.(*ext.TableCell); ok {
				alignment = c.Alignment
			}
			l.inline(l.inlineParts(cell, st), x1+pad, x2-pad, alignment)
			l.y += pad
			bottom = max(bottom, l.y)
			// Background is extended after every cell's height is known.
			l.doc.ops[start].rect.Max.Y = -1
			col++
		}
		for i := len(l.doc.ops) - 1; i >= 0; i-- {
			op := &l.doc.ops[i]
			if op.rect.Min.Y < top {
				break
			}
			if op.rect.Max.Y == -1 {
				op.rect.Max.Y = bottom
			}
		}
		for i := 0; i <= cols; i++ {
			x := left + (right-left)*i/cols
			l.fill(image.Rect(x, top, x+1, bottom), l.doc.colors.line)
		}
		l.fill(image.Rect(left, top, right, top+1), l.doc.colors.line)
		l.fill(image.Rect(left, bottom-1, right, bottom), l.doc.colors.line)
		l.y = bottom
	}
	l.y += l.gap
}

func (l *layout) imageBlock(n *ast.Image, left, right int, st textStyle) {
	// Linked images, including their error placeholders, are not text links.
	st.link = 0
	alt := safeText(string(n.Text(l.source)))
	img, err := l.localImage(string(n.Destination), max(1, right-left))
	if err != nil {
		st.ink = l.doc.colors.muted
		l.inline([]inlinePart{{text: fmt.Sprintf("[Image: %s] %s", alt, err), style: st}}, left, right, 0)
		return
	}
	r := image.Rect(left, l.y, left+img.Bounds().Dx(), l.y+img.Bounds().Dy())
	l.addOp(paintOp{rect: r, picture: img})
	l.y = r.Max.Y + l.gap
}
func (l *layout) localImage(destination string, width int) (image.Image, error) {
	u, err := url.Parse(html.UnescapeString(destination))
	if err != nil {
		return nil, fmt.Errorf("invalid path")
	}
	if u.Scheme != "" || u.Host != "" || strings.HasPrefix(destination, "//") {
		return nil, fmt.Errorf("remote images are not supported")
	}
	path := u.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(l.baseDir, path)
	}
	key := fmt.Sprintf("%s:%d", path, width)
	if cached := l.images[key]; cached != nil {
		return cached, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read image: %s", safeText(err.Error()))
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return nil, fmt.Errorf("image file is too large or is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("unsupported format or corrupt image")
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return nil, fmt.Errorf("unsupported image format")
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if cfg.Width < 1 || cfg.Height < 1 || pixels > maxImagePixels || l.imagePixels+pixels > maxRenderPixels {
		return nil, fmt.Errorf("image exceeds the pixel limit")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image")
	}
	if img.Bounds().Dx() > width {
		h := max(1, img.Bounds().Dy()*width/img.Bounds().Dx())
		scaled := image.NewRGBA(image.Rect(0, 0, width, h))
		xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Over, nil)
		img = scaled
	}
	l.imagePixels += pixels
	l.images[key] = img
	return img, nil
}

func (d *document) paint(offset, height int) *image.RGBA {
	img := d.paintBase(offset, height)
	d.drawDots(img, image.Pt(0, offset), staticDotsPhase)
	return img
}
func (d *document) paintBase(offset, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, d.width, max(1, height)))
	draw.Draw(img, img.Bounds(), image.NewUniform(d.colors.bg), image.Point{}, draw.Src)
	checkboxes := make(map[checkboxKey]*image.RGBA)
	for _, op := range d.ops {
		r := op.rect.Sub(image.Pt(0, offset))
		if !r.Overlaps(img.Bounds()) {
			continue
		}
		if op.radius > 0 {
			paintRoundedRect(img, r, op.radius, op.ink)
			continue
		}
		if op.checkbox != nil {
			key := checkboxKey{size: r.Dx(), state: *op.checkbox}
			box := checkboxes[key]
			if box == nil {
				box = checkboxImage(key.size, key.state, d.colors)
				checkboxes[key] = box
			}
			draw.Draw(img, r, box, image.Point{}, draw.Over)
			continue
		}
		if op.picture != nil {
			draw.Draw(img, r, op.picture, op.picture.Bounds().Min, draw.Over)
			continue
		}
		if op.text == "" {
			draw.Draw(img, r, image.NewUniform(op.ink), image.Point{}, draw.Src)
			continue
		}
		st := op.style
		ch := []rune(op.text)[0]
		face := d.fonts.runeFace(st.size, st.bold, st.code && ch < 128, ch)
		baselineOffset := st.baseline
		if baselineOffset == 0 {
			baselineOffset = d.fonts.baseline(st.size)
		}
		baseline := r.Min.Y + baselineOffset
		if st.code {
			top, height := r.Min.Y, st.size*3/2
			if st.lineHeight > 0 {
				top += max(0, baselineOffset-st.size)
				height = st.lineHeight
			}
			draw.Draw(img, image.Rect(r.Min.X, top, r.Max.X, top+height), image.NewUniform(d.colors.panel), image.Point{}, draw.Src)
		}
		drawer := font.Drawer{Dst: img, Src: image.NewUniform(st.ink), Face: face, Dot: fixed.P(r.Min.X, baseline)}
		drawer.DrawString(op.text)
		for _, line := range []struct {
			enabled bool
			y       int
		}{{st.underline, baseline + 2}, {st.strike, baseline - st.size/3}} {
			if line.enabled {
				draw.Draw(img, image.Rect(r.Min.X, line.y, r.Max.X, line.y+max(1, st.size/16)), image.NewUniform(st.ink), image.Point{}, draw.Src)
			}
		}
	}
	return img
}

func writePNG(path string, img image.Image) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".rex-md-png-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	err = encoder.Encode(f, img)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
