package filegentool

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/signintech/gopdf"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extAst "github.com/yuin/goldmark/extension/ast"
	gmText "github.com/yuin/goldmark/text"
)

// ============================================================================
// Font Configuration
// ============================================================================

// FontConfig holds font configuration for PDF rendering.
// CJK fonts cover both CJK and Latin glyphs, so they are registered under
// all font name slots (regular, bold, italic, bolditalic, mono) as well.
type FontConfig struct {
	// CJKFontPath is the file path to a TTF font that supports CJK characters.
	// Required. A CJK font (e.g. NotoSansSC) typically covers Latin glyphs too,
	// so it serves as the single font source for the entire PDF.
	CJKFontPath string

	// CJKBoldFontPath is the file path to a bold-weight CJK font.
	// If empty, the regular CJK font is used for bold text as well.
	CJKBoldFontPath string
}

// ============================================================================
// PDF Validation
// ============================================================================

// validatePDF validates PDF bytes using pdfcpu.
func validatePDF(data []byte) error {
	rd := bytes.NewReader(data)
	return api.Validate(rd, nil)
}

// ============================================================================
// Layout constants (all in points, A4 = 595×842 pt)
// ============================================================================

const (
	pageWidthPt  = 595.28
	pageHeightPt = 841.89
	marginLeft   = 56.0 // ~2 cm
	marginRight  = 56.0
	marginTop    = 56.0
	marginBottom = 56.0
	contentWidth = pageWidthPt - marginLeft - marginRight

	defaultBodySize = 11.0
	defaultCodeSize = 9.5
	smallGap        = 4.0
	mediumGap       = 8.0
	largeGap        = 14.0
	codePadX        = 6.0
	codePadY        = 4.0

	fontRegular    = "regular"
	fontBold       = "bold"
	fontItalic     = "italic"
	fontBoldItalic = "bolditalic"
	fontMono       = "mono"
	fontCJK        = "cjk"
	fontCJKBold    = "cjk_bold"
)

// headingSizes maps heading levels (1–6) to font sizes in points.
var headingSizes = map[int]float64{
	1: 24, 2: 20, 3: 17, 4: 14, 5: 12, 6: 11,
}

// ============================================================================
// renderMarkdownToPDF — Markdown → PDF generation
// ============================================================================

// renderMarkdownToPDF parses markdown source and returns raw PDF bytes,
// using the provided font configuration.
func renderMarkdownToPDF(mdSource string, fc *FontConfig) ([]byte, error) {
	if fc == nil || fc.CJKFontPath == "" {
		return nil, fmt.Errorf("font configuration is required: cjkFontPath must be set")
	}

	src := []byte(mdSource)

	// 1. Parse Markdown → AST (with table extension)
	md := goldmark.New(goldmark.WithExtensions(extension.NewTable()))
	reader := gmText.NewReader(src)
	doc := md.Parser().Parse(reader)

	// 2. Initialise PDF
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{
		Unit:     gopdf.UnitPT,
		PageSize: *gopdf.PageSizeA4,
	})

	// 3. Load CJK fonts (serves as the sole font source — covers Latin + CJK glyphs)
	if err := loadCJKFonts(pdf, fc); err != nil {
		return nil, fmt.Errorf("load fonts: %w", err)
	}

	pdf.SetMargins(marginLeft, marginTop, marginRight, marginBottom)

	r := &pdfRenderer{
		pdf:        pdf,
		curX:       marginLeft,
		curY:       marginTop,
		styleStack: []textStyle{{font: fontRegular, size: defaultBodySize}},
		source:     src,
	}
	r.pdf.AddPage()

	// 4. Walk AST
	if err := r.renderNode(doc); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}

	// 5. Extract bytes
	return pdf.GetBytesPdfReturnErr()
}

// loadCJKFonts loads CJK TTF fonts and registers them under all font name
// slots. A CJK font (e.g. NotoSansSC) covers both CJK and Latin glyphs, so
// a single font file is sufficient for the entire PDF.
func loadCJKFonts(pdf *gopdf.GoPdf, fc *FontConfig) error {
	boldPath := fc.CJKBoldFontPath
	if boldPath == "" {
		boldPath = fc.CJKFontPath
	}

	// Register regular-weight font under all non-bold slots
	for _, name := range []string{fontCJK, fontRegular, fontItalic, fontMono} {
		if err := pdf.AddTTFFont(name, fc.CJKFontPath); err != nil {
			return fmt.Errorf("add font %s (%s): %w", name, fc.CJKFontPath, err)
		}
	}

	// Register bold-weight font under all bold slots
	for _, name := range []string{fontCJKBold, fontBold, fontBoldItalic} {
		if err := pdf.AddTTFFont(name, boldPath); err != nil {
			return fmt.Errorf("add font %s (%s): %w", name, boldPath, err)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// pdfRenderer
// ---------------------------------------------------------------------------

// pdfRenderer walks a goldmark AST and draws elements onto a gopdf page.
type pdfRenderer struct {
	pdf        *gopdf.GoPdf
	curX       float64
	curY       float64
	styleStack []textStyle
	source     []byte
}

// textStyle describes the current font style (weight, size, posture) for text rendering.
type textStyle struct {
	font   string
	size   float64
	bold   bool
	italic bool
}

// fontName returns the registered font name to use for the current style.
// Bold text maps to the CJK bold font; all other weights use the regular CJK font.
func (s textStyle) fontName() string {
	if s.bold {
		return fontCJKBold
	}
	return fontCJK
}

// curStyle returns the current text style from the top of the style stack.
func (r *pdfRenderer) curStyle() textStyle {
	if len(r.styleStack) == 0 {
		return textStyle{font: fontRegular, size: defaultBodySize}
	}
	return r.styleStack[len(r.styleStack)-1]
}

// pushStyle pushes a new text style onto the style stack.
func (r *pdfRenderer) pushStyle(s textStyle) {
	r.styleStack = append(r.styleStack, s)
}

// popStyle pops the top text style off the style stack.
func (r *pdfRenderer) popStyle() {
	if len(r.styleStack) > 1 {
		r.styleStack = r.styleStack[:len(r.styleStack)-1]
	}
}

// applyStyle sets the gopdf font to match the given text style.
func (r *pdfRenderer) applyStyle(s textStyle) error {
	return r.pdf.SetFont(s.fontName(), "", s.size)
}

// ---------------------------------------------------------------------------
// CJK detection (for line-breaking strategy only, not font selection)
// ---------------------------------------------------------------------------

func containsCJK(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hangul, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Thai, r) ||
			unicode.Is(unicode.Arabic, r) ||
			unicode.Is(unicode.Devanagari, r) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Emoji stripping — remove characters that gopdf cannot render
// ---------------------------------------------------------------------------

// isEmojiRune reports whether r is in a Unicode range commonly used for
// emoji. These characters are silently skipped by gopdf (leaving blank
// gaps), so we strip them before rendering to avoid visual artefacts.
func isEmojiRune(r rune) bool {
	switch {
	// Emoticons
	case r >= 0x1F600 && r <= 0x1F64F:
		return true
	// Miscellaneous Symbols and Pictographs
	case r >= 0x1F300 && r <= 0x1F5FF:
		return true
	// Transport and Map Symbols
	case r >= 0x1F680 && r <= 0x1F6FF:
		return true
	// Supplemental Symbols and Pictographs
	case r >= 0x1F900 && r <= 0x1F9FF:
		return true
	// Symbols and Pictographs Extended-A
	case r >= 0x1FA00 && r <= 0x1FAFF:
		return true
	// Miscellaneous Symbols
	case r >= 0x2600 && r <= 0x26FF:
		return true
	// Dingbats
	case r >= 0x2702 && r <= 0x27B0:
		return true
	// Enclosed Alphanumeric Supplement (flag emojis)
	case r >= 0x1F100 && r <= 0x1F1FF:
		return true
	// Enclosed Ideographic Supplement
	case r >= 0x1F200 && r <= 0x1F2FF:
		return true
	// Mahjong Tiles, Domino, Playing Cards
	case r >= 0x1F000 && r <= 0x1F0FF:
		return true
	// Alchemical Symbols, Geometric Shapes Extended, Supplemental Arrows-C
	case r >= 0x1F700 && r <= 0x1F8FF:
		return true
	// Variation Selector-16 (emoji presentation)
	case r == 0xFE0F:
		return true
	// Combining Enclosing Keycap
	case r == 0x20E3:
		return true
	// Zero Width Joiner (used in emoji sequences)
	case r == 0x200D:
		return true
	// Tag characters (used in flag emoji sequences)
	case r >= 0xE0020 && r <= 0xE007F:
		return true
	// Common individual emoji codepoints
	case r == 0x203C || r == 0x2049: // ‼ ⁉
		return true
	case r == 0x2122 || r == 0x2139: // ™ ℹ
		return true
	case r >= 0x2194 && r <= 0x2199: // arrows
		return true
	case r == 0x21A9 || r == 0x21AA:
		return true
	case r >= 0x231A && r <= 0x231B: // watch, hourglass
		return true
	case r == 0x2328: // keyboard
		return true
	case r == 0x23CF: // eject
		return true
	case r >= 0x23E9 && r <= 0x23F3:
		return true
	case r >= 0x23F8 && r <= 0x23FA:
		return true
	case r == 0x24C2:
		return true
	case r >= 0x25AA && r <= 0x25AB:
		return true
	case r == 0x25B6 || r == 0x25C0:
		return true
	case r >= 0x25FB && r <= 0x25FE:
		return true
	// 0x2600–0x26FF and 0x2702–0x27B0 are already covered by the wide
	// ranges above (Miscellaneous Symbols and Dingbats), so the specific
	// codepoints within those ranges are not listed here.
	case r >= 0x2934 && r <= 0x2935:
		return true
	case r >= 0x2B05 && r <= 0x2B07:
		return true
	case r == 0x2B1B || r == 0x2B1C:
		return true
	case r == 0x2B50 || r == 0x2B55:
		return true
	case r == 0x3030 || r == 0x303D:
		return true
	case r == 0x3297 || r == 0x3299:
		return true
	}
	return false
}

// stripEmoji removes emoji characters from text that gopdf cannot render,
// preventing blank gaps in the PDF output.
func stripEmoji(text string) string {
	if !containsEmoji(text) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if !isEmojiRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// containsEmoji performs a quick check for whether text contains any emoji rune.
func containsEmoji(text string) bool {
	for _, r := range text {
		if isEmojiRune(r) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Page management
// ---------------------------------------------------------------------------

// ensureSpace adds a new page if the remaining vertical space is less than needed.
func (r *pdfRenderer) ensureSpace(needed float64) {
	if r.curY+needed > pageHeightPt-marginBottom {
		r.pdf.AddPage()
		r.curX = marginLeft
		r.curY = marginTop
	}
}

// moveDown advances the vertical cursor and ensures there is at least some space left.
func (r *pdfRenderer) moveDown(pts float64) {
	r.curY += pts
	r.ensureSpace(0)
}

// ---------------------------------------------------------------------------
// Text drawing
// ---------------------------------------------------------------------------

// writeLine renders a line of text with automatic word/character wrapping.
// It selects word-based wrapping for Latin text and character-based wrapping
// for CJK text. Returns the total height consumed.
func (r *pdfRenderer) writeLine(text string, width float64) (float64, error) {
	if text == "" {
		text = " "
	}
	s := r.curStyle()
	if err := r.applyStyle(s); err != nil {
		return 0, err
	}
	lineH := s.size * 1.4

	if containsCJK(text) {
		return r.writeLineByChar(text, width, lineH)
	}
	return r.writeLineByWord(text, width, lineH)
}

// writeLineByWord renders text using word-based line breaking (Latin text).
// Returns the total height consumed.
func (r *pdfRenderer) writeLineByWord(text string, width float64, lineH float64) (float64, error) {
	totalH := 0.0
	words := strings.Fields(text)
	if len(words) == 0 {
		return lineH, nil
	}

	lineBuf := ""
	for _, word := range words {
		candidate := lineBuf
		if candidate != "" {
			candidate += " "
		}
		candidate += word

		w, err := r.pdf.MeasureTextWidth(candidate)
		if err != nil {
			return totalH, err
		}
		if w > width && lineBuf != "" {
			r.ensureSpace(lineH)
			r.pdf.SetXY(r.curX, r.curY)
			if err := r.pdf.Cell(&gopdf.Rect{W: width, H: lineH}, lineBuf); err != nil {
				return totalH, err
			}
			r.curY += lineH
			totalH += lineH
			lineBuf = word
		} else {
			lineBuf = candidate
		}
	}

	if lineBuf != "" {
		r.ensureSpace(lineH)
		r.pdf.SetXY(r.curX, r.curY)
		if err := r.pdf.Cell(&gopdf.Rect{W: width, H: lineH}, lineBuf); err != nil {
			return totalH, err
		}
		r.curY += lineH
		totalH += lineH
	}
	return totalH, nil
}

// writeLineByChar renders text using character-based line breaking (CJK text).
// Returns the total height consumed.
func (r *pdfRenderer) writeLineByChar(text string, width float64, lineH float64) (float64, error) {
	totalH := 0.0
	runes := []rune(text)

	charWidths := make([]float64, len(runes))
	for i, ch := range runes {
		w, err := r.pdf.MeasureTextWidth(string(ch))
		if err != nil {
			return totalH, err
		}
		charWidths[i] = w
	}

	lineStart := 0
	curWidth := 0.0

	for i, w := range charWidths {
		if curWidth+w > width && i > lineStart {
			lineText := string(runes[lineStart:i])
			r.ensureSpace(lineH)
			r.pdf.SetXY(r.curX, r.curY)
			if err := r.pdf.Cell(&gopdf.Rect{W: width, H: lineH}, lineText); err != nil {
				return totalH, err
			}
			r.curY += lineH
			totalH += lineH
			lineStart = i
			curWidth = w
		} else {
			curWidth += w
		}
	}

	if lineStart < len(runes) {
		lineText := string(runes[lineStart:])
		r.ensureSpace(lineH)
		r.pdf.SetXY(r.curX, r.curY)
		if err := r.pdf.Cell(&gopdf.Rect{W: width, H: lineH}, lineText); err != nil {
			return totalH, err
		}
		r.curY += lineH
		totalH += lineH
	}
	return totalH, nil
}

// writeLineCentered renders text with center alignment within the given width.
// Used for H1 headings. Returns the total height consumed.
func (r *pdfRenderer) writeLineCentered(text string, width float64) (float64, error) {
	if text == "" {
		text = " "
	}
	s := r.curStyle()
	if err := r.applyStyle(s); err != nil {
		return 0, err
	}
	lineH := s.size * 1.4

	lines := r.splitLines(text, width, s)

	totalH := 0.0
	for _, line := range lines {
		lw, err := r.pdf.MeasureTextWidth(line)
		if err != nil {
			return totalH, err
		}
		x := r.curX + (width-lw)/2
		if x < r.curX {
			x = r.curX
		}
		r.ensureSpace(lineH)
		r.pdf.SetXY(x, r.curY)
		if err := r.pdf.Cell(&gopdf.Rect{W: width, H: lineH}, line); err != nil {
			return totalH, err
		}
		r.curY += lineH
		totalH += lineH
	}
	return totalH, nil
}

// splitLines splits text into lines that fit within width, using word-based or
// character-based strategy depending on the content language.
func (r *pdfRenderer) splitLines(text string, width float64, s textStyle) []string {
	if containsCJK(text) {
		return r.splitLinesByChar(text, width, s)
	}
	return r.splitLinesByWord(text, width, s)
}

// splitLinesByWord splits text into lines using word-based wrapping (Latin text).
func (r *pdfRenderer) splitLinesByWord(text string, width float64, s textStyle) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	lineBuf := ""
	for _, word := range words {
		candidate := lineBuf
		if candidate != "" {
			candidate += " "
		}
		candidate += word
		if err := r.applyStyle(s); err != nil {
			return lines
		}
		w, err := r.pdf.MeasureTextWidth(candidate)
		if err != nil {
			return lines
		}
		if w > width && lineBuf != "" {
			lines = append(lines, lineBuf)
			lineBuf = word
		} else {
			lineBuf = candidate
		}
	}
	if lineBuf != "" {
		lines = append(lines, lineBuf)
	}
	return lines
}

// splitLinesByChar splits text into lines using character-based wrapping (CJK text).
func (r *pdfRenderer) splitLinesByChar(text string, width float64, s textStyle) []string {
	runes := []rune(text)
	charWidths := make([]float64, len(runes))
	for i, ch := range runes {
		if err := r.applyStyle(s); err != nil {
			return nil
		}
		w, err := r.pdf.MeasureTextWidth(string(ch))
		if err != nil {
			return nil
		}
		charWidths[i] = w
	}

	var lines []string
	lineStart := 0
	curWidth := 0.0
	for i, w := range charWidths {
		if curWidth+w > width && i > lineStart {
			lines = append(lines, string(runes[lineStart:i]))
			lineStart = i
			curWidth = w
		} else {
			curWidth += w
		}
	}
	if lineStart < len(runes) {
		lines = append(lines, string(runes[lineStart:]))
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// ---------------------------------------------------------------------------
// AST rendering
// ---------------------------------------------------------------------------

// renderNode dispatches rendering based on the AST node type.
func (r *pdfRenderer) renderNode(n ast.Node) error {
	switch node := n.(type) {
	case *ast.Document:
		return r.renderChildren(n)
	case *ast.Heading:
		return r.renderHeading(node)
	case *ast.Paragraph:
		return r.renderParagraph(node)
	case *ast.Text:
		return nil
	case *ast.String:
		return nil
	case *ast.Emphasis:
		return r.renderEmphasis(node)
	case *ast.CodeSpan:
		return r.renderCodeSpan(node)
	case *ast.FencedCodeBlock:
		return r.renderCodeBlock(node)
	case *ast.List:
		return r.renderList(node)
	case *ast.ListItem:
		return r.renderListItem(node)
	case *ast.TextBlock:
		return r.renderChildren(n)
	case *ast.ThematicBreak:
		return r.renderThematicBreak()
	case *ast.Link:
		return r.renderLink(node)
	case *extAst.Table:
		return r.renderTable(node)
	case *extAst.TableRow:
		return nil
	case *extAst.TableCell:
		return nil
	default:
		return r.renderChildren(n)
	}
}

// renderChildren recursively renders all child nodes of the given AST node.
func (r *pdfRenderer) renderChildren(n ast.Node) error {
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if err := r.renderNode(child); err != nil {
			return err
		}
	}
	return nil
}

// renderHeading renders a heading node with appropriate size and alignment
// (H1 is centered, H2–H6 are left-aligned with bottom border).
func (r *pdfRenderer) renderHeading(h *ast.Heading) error {
	r.curX = marginLeft
	r.moveDown(largeGap)
	size, ok := headingSizes[h.Level]
	if !ok {
		size = defaultBodySize
	}
	st := textStyle{font: fontRegular, size: size, bold: true}
	r.pushStyle(st)
	defer r.popStyle()

	text := r.collectInlineText(h)
	if h.Level == 1 {
		if _, err := r.writeLineCentered(text, contentWidth); err != nil {
			return err
		}
	} else {
		if _, err := r.writeLine(text, contentWidth); err != nil {
			return err
		}
	}
	r.moveDown(smallGap)
	return nil
}

// renderParagraph renders a paragraph node with automatic line wrapping.
func (r *pdfRenderer) renderParagraph(p *ast.Paragraph) error {
	r.curX = marginLeft
	text := r.collectInlineText(p)
	if text == "" {
		return nil
	}
	h, err := r.writeLine(text, contentWidth)
	if err != nil {
		return err
	}
	if h > 0 {
		r.moveDown(smallGap)
	}
	return nil
}

// renderEmphasis renders bold (double delimiter) or italic (single delimiter) emphasis.
func (r *pdfRenderer) renderEmphasis(e *ast.Emphasis) error {
	parent := r.curStyle()
	st := parent
	if e.Level == 2 {
		st.bold = true
	} else {
		st.italic = true
	}
	r.pushStyle(st)
	defer r.popStyle()
	return r.renderChildren(e)
}

// renderCodeSpan renders inline code with a light grey background and monospace font.
func (r *pdfRenderer) renderCodeSpan(cs *ast.CodeSpan) error {
	text := r.collectInlineText(cs)
	st := textStyle{font: fontMono, size: r.curStyle().size, bold: false, italic: false}
	r.pushStyle(st)
	defer r.popStyle()
	if err := r.applyStyle(st); err != nil {
		return err
	}

	tw, _ := r.pdf.MeasureTextWidth(text)
	pad := 3.0
	r.ensureSpace(st.size * 1.4)

	x0 := r.curX
	y0 := r.curY
	bgH := st.size * 1.4
	r.pdf.SetFillColor(240, 240, 240)
	r.pdf.Rectangle(x0, y0, x0+tw+pad*2, y0+bgH, "F", 0, 0)

	r.pdf.SetXY(x0+pad, y0)
	r.pdf.SetTextColor(50, 50, 50)
	if err := r.pdf.Cell(&gopdf.Rect{W: tw + pad, H: bgH}, text); err != nil {
		return err
	}
	r.pdf.SetTextColor(0, 0, 0)
	r.curX = x0 + tw + pad*2
	return nil
}

// renderCodeBlock renders a fenced code block with a grey background and monospace font.
func (r *pdfRenderer) renderCodeBlock(cb *ast.FencedCodeBlock) error {
	r.moveDown(mediumGap)

	st := textStyle{font: fontMono, size: defaultCodeSize, bold: false, italic: false}
	r.pushStyle(st)
	defer r.popStyle()

	if err := r.applyStyle(st); err != nil {
		return err
	}

	lineH := st.size * 1.5
	var lines []string
	for i := 0; i < cb.Lines().Len(); i++ {
		seg := cb.Lines().At(i)
		line := strings.TrimRight(string(seg.Value(r.source)), "\r\n")
		lines = append(lines, stripEmoji(line))
	}

	totalH := float64(len(lines))*lineH + codePadY*2
	r.ensureSpace(totalH)

	bgX := marginLeft
	bgY := r.curY
	r.pdf.SetFillColor(245, 245, 245)
	r.pdf.Rectangle(bgX, bgY, bgX+contentWidth, bgY+totalH, "F", 0, 0)

	r.curX = bgX + codePadX
	r.curY = bgY + codePadY

	r.pdf.SetTextColor(40, 40, 40)
	for _, line := range lines {
		r.pdf.SetXY(r.curX, r.curY)
		if err := r.pdf.Cell(&gopdf.Rect{W: contentWidth - codePadX*2, H: lineH}, line); err != nil {
			return err
		}
		r.curY += lineH
	}
	r.pdf.SetTextColor(0, 0, 0)

	r.curX = marginLeft
	r.moveDown(mediumGap)
	return nil
}

// renderList renders an ordered or unordered list with proper indentation.
func (r *pdfRenderer) renderList(l *ast.List) error {
	r.moveDown(smallGap)
	err := r.renderChildren(l)
	r.curX = marginLeft
	r.moveDown(smallGap)
	return err
}

// renderListItem renders a single list item with a bullet or number marker.
func (r *pdfRenderer) renderListItem(li *ast.ListItem) error {
	parentList := li.Parent()
	list, ok := parentList.(*ast.List)
	if !ok {
		return r.renderChildren(li)
	}

	r.curX = marginLeft + 20

	marker := "• "
	if list.IsOrdered() {
		idx := 1
		for s := li.PreviousSibling(); s != nil; s = s.PreviousSibling() {
			idx++
		}
		marker = fmt.Sprintf("%d. ", idx)
	}

	st := r.curStyle()
	if err := r.applyStyle(st); err != nil {
		return err
	}
	markerW, _ := r.pdf.MeasureTextWidth(marker)

	r.ensureSpace(st.size * 1.4)
	r.pdf.SetXY(r.curX, r.curY)
	if err := r.pdf.Cell(&gopdf.Rect{W: markerW, H: st.size * 1.4}, marker); err != nil {
		return err
	}

	oldX := r.curX
	r.curX += markerW + 2

	text := r.collectInlineText(li)
	if text != "" {
		if _, err := r.writeLine(text, contentWidth-20-markerW-2); err != nil {
			return err
		}
	}

	r.curX = oldX
	r.moveDown(2)
	return nil
}

// renderThematicBreak renders a horizontal rule (---) as a thin grey line.
func (r *pdfRenderer) renderThematicBreak() error {
	r.moveDown(mediumGap)
	r.pdf.SetLineWidth(0.5)
	r.pdf.SetStrokeColor(180, 180, 180)
	r.pdf.Line(marginLeft, r.curY, marginLeft+contentWidth, r.curY)
	r.pdf.SetStrokeColor(0, 0, 0)
	r.moveDown(mediumGap)
	return nil
}

// renderLink renders hyperlink text in blue italic.
func (r *pdfRenderer) renderLink(l *ast.Link) error {
	text := r.collectInlineText(l)
	st := r.curStyle()
	st.italic = true
	r.pushStyle(st)
	defer r.popStyle()
	r.pdf.SetTextColor(0, 90, 200)
	if _, err := r.writeLine(text, contentWidth); err != nil {
		return err
	}
	r.pdf.SetTextColor(0, 0, 0)
	return nil
}

// ---------------------------------------------------------------------------
// Table
// ---------------------------------------------------------------------------

const (
	tableFontSize = 10.0
	tableCellPadX = 6.0
	tableMinColW  = 30.0
	tableMaxColW  = contentWidth * 0.62
)

// tableRow represents a single row in a table, with a flag indicating whether
// it is a header row.
type tableRow struct {
	cells    []string
	isHeader bool
}

// collectTableRows extracts all rows (header and data) from a table AST node.
func (r *pdfRenderer) collectTableRows(t *extAst.Table) []tableRow {
	var rows []tableRow
	for child := t.FirstChild(); child != nil; child = child.NextSibling() {
		switch v := child.(type) {
		case *extAst.TableHeader:
			var cells []string
			for cell := v.FirstChild(); cell != nil; cell = cell.NextSibling() {
				tc, ok := cell.(*extAst.TableCell)
				if !ok {
					continue
				}
				cells = append(cells, r.collectInlineText(tc))
			}
			rows = append(rows, tableRow{cells: cells, isHeader: true})
		case *extAst.TableRow:
			var cells []string
			for cell := v.FirstChild(); cell != nil; cell = cell.NextSibling() {
				tc, ok := cell.(*extAst.TableCell)
				if !ok {
					continue
				}
				cells = append(cells, r.collectInlineText(tc))
			}
			rows = append(rows, tableRow{cells: cells, isHeader: false})
		}
	}
	return rows
}

// renderTable renders a table with auto-fitted column widths, header
// highlighting, and automatic text wrapping within cells.
func (r *pdfRenderer) renderTable(t *extAst.Table) error {
	r.curX = marginLeft
	r.moveDown(mediumGap)
	r.ensureSpace(30)

	rows := r.collectTableRows(t)
	if len(rows) == 0 {
		return nil
	}

	cellsOnly := make([][]string, len(rows))
	for i, row := range rows {
		cellsOnly[i] = row.cells
	}
	numCols := 0
	for _, row := range cellsOnly {
		if len(row) > numCols {
			numCols = len(row)
		}
	}

	bodySt := textStyle{font: fontRegular, size: tableFontSize, bold: false}
	boldSt := textStyle{font: fontRegular, size: tableFontSize, bold: true}

	colW := r.computeColumnWidths(cellsOnly, numCols, bodySt, boldSt)

	singleLineH := tableFontSize * 1.5
	r.pdf.SetTextColor(0, 0, 0)

	for _, row := range rows {
		isHeader := row.isHeader

		var cellLines [][]string
		maxLines := 1
		for ci := 0; ci < numCols; ci++ {
			cellText := ""
			if ci < len(row.cells) {
				cellText = row.cells[ci]
			}
			st := bodySt
			if isHeader {
				st = boldSt
			}
			lines := r.splitLines(cellText, colW[ci]-tableCellPadX, st)
			if len(lines) > maxLines {
				maxLines = len(lines)
			}
			cellLines = append(cellLines, lines)
		}
		rowH := singleLineH * float64(maxLines)

		r.ensureSpace(rowH)

		if isHeader {
			r.pdf.SetFillColor(230, 230, 230)
			r.pdf.Rectangle(marginLeft, r.curY, marginLeft+contentWidth, r.curY+rowH, "F", 0, 0)
			r.pdf.SetTextColor(0, 0, 0)
		}

		cx := marginLeft
		for ci := 0; ci < numCols; ci++ {
			lines := cellLines[ci]
			st := bodySt
			if isHeader {
				st = boldSt
			}
			if err := r.applyStyle(st); err != nil {
				return err
			}

			cellInnerW := colW[ci] - tableCellPadX
			blockH := singleLineH * float64(len(lines))
			topInset := (rowH - blockH) / 2
			lineY := r.curY + topInset

			for _, line := range lines {
				r.ensureSpace(singleLineH)
				r.pdf.SetXY(cx+tableCellPadX/2, lineY)
				if err := r.pdf.CellWithOption(&gopdf.Rect{W: cellInnerW, H: singleLineH}, line, gopdf.CellOption{Align: gopdf.Left | gopdf.Middle}); err != nil {
					return err
				}
				lineY += singleLineH
			}
			cx += colW[ci]
		}

		r.pdf.SetLineWidth(0.3)
		r.pdf.SetStrokeColor(200, 200, 200)
		r.pdf.Line(marginLeft, r.curY+rowH, marginLeft+contentWidth, r.curY+rowH)
		r.pdf.SetStrokeColor(0, 0, 0)

		r.curY += rowH
	}

	r.moveDown(mediumGap)
	return nil
}

// computeColumnWidths calculates proportional column widths that fit within
// the content area, distributing spare space or shrinking as needed.
func (r *pdfRenderer) computeColumnWidths(rows [][]string, numCols int, bodySt, boldSt textStyle) []float64 {
	natW := make([]float64, numCols)
	for ri, row := range rows {
		isHeader := ri == 0
		for ci := 0; ci < numCols; ci++ {
			cellText := ""
			if ci < len(row) {
				cellText = row[ci]
			}
			st := bodySt
			if isHeader {
				st = boldSt
			}
			if err := r.applyStyle(st); err != nil {
				continue
			}
			w, err := r.pdf.MeasureTextWidth(cellText)
			if err != nil {
				continue
			}
			needed := w + tableCellPadX
			if needed > natW[ci] {
				natW[ci] = needed
			}
		}
	}

	for i := range natW {
		if natW[i] > tableMaxColW {
			natW[i] = tableMaxColW
		}
		if natW[i] < tableMinColW {
			natW[i] = tableMinColW
		}
	}

	total := 0.0
	for _, w := range natW {
		total += w
	}

	colW := make([]float64, numCols)
	if total <= contentWidth {
		spare := contentWidth - total
		if spare > 0 {
			weightSum := 0.0
			weights := make([]float64, numCols)
			for i, w := range natW {
				weights[i] = w - tableMinColW
				weightSum += weights[i]
			}
			if weightSum > 0 {
				for i := range natW {
					natW[i] += spare * (weights[i] / weightSum)
				}
			} else {
				each := spare / float64(numCols)
				for i := range natW {
					natW[i] += each
				}
			}
		}
		copy(colW, natW)
	} else {
		copy(colW, natW)
		for {
			sum := 0.0
			for _, w := range colW {
				sum += w
			}
			if sum <= contentWidth+0.01 {
				break
			}
			shrinkable := 0.0
			for _, w := range colW {
				if w > tableMinColW {
					shrinkable += w - tableMinColW
				}
			}
			over := sum - contentWidth
			if shrinkable <= 0 {
				break
			}
			factor := over / shrinkable
			if factor > 1 {
				factor = 1
			}
			for i := range colW {
				if colW[i] > tableMinColW {
					colW[i] -= (colW[i] - tableMinColW) * factor
				}
			}
		}
	}

	sum := 0.0
	for _, w := range colW {
		sum += w
	}
	if numCols > 0 && sum != contentWidth {
		colW[numCols-1] += contentWidth - sum
		if colW[numCols-1] < tableMinColW {
			colW[numCols-1] = tableMinColW
		}
	}
	return colW
}

// ---------------------------------------------------------------------------
// Inline text collection
// ---------------------------------------------------------------------------

// collectInlineText collects all inline text content from an AST node,
// stripping emoji characters that gopdf cannot render.
func (r *pdfRenderer) collectInlineText(n ast.Node) string {
	var buf strings.Builder
	r.collectTextInto(n, &buf)
	return stripEmoji(buf.String())
}

// collectTextInto recursively writes the text content of an AST node into buf,
// preserving inline code backticks and line break semantics.
func (r *pdfRenderer) collectTextInto(n ast.Node, buf *strings.Builder) {
	switch node := n.(type) {
	case *ast.Text:
		buf.Write(node.Segment.Value(r.source))
		if node.SoftLineBreak() {
			buf.WriteByte(' ')
		}
		if node.HardLineBreak() {
			buf.WriteByte('\n')
		}
	case *ast.String:
		buf.Write(node.Value)
	case *ast.CodeSpan:
		buf.WriteString("`")
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			r.collectTextInto(child, buf)
		}
		buf.WriteString("`")
	default:
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			r.collectTextInto(child, buf)
		}
	}
}
