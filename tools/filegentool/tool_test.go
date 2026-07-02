package filegentool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extAst "github.com/yuin/goldmark/extension/ast"
	gmText "github.com/yuin/goldmark/text"
)

// ----------------------------------------------------------------------------
// Test helpers
// ----------------------------------------------------------------------------

// testFontConfig returns a FontConfig for testing.
// Uses the font in the testdata/ directory; returns nil if not found
// (PDF tests will be skipped).
func testFontConfig() *FontConfig {
	fc := &FontConfig{
		CJKFontPath: "testdata/font.ttf",
	}
	if _, err := os.Stat(fc.CJKFontPath); err != nil {
		return nil
	}
	return fc
}

// skipIfNoFont skips the test if the CJK font is not available.
func skipIfNoFont(t *testing.T) {
	t.Helper()
	if testFontConfig() == nil {
		t.Skip("Skipping: CJK font not available at testdata/font.ttf")
	}
}

// dumpFile writes bytes to a temp file for manual inspection.
// Set FILEGENTOOL_DUMP=1 to enable.
func dumpFile(t *testing.T, name string, data []byte) {
	if os.Getenv("FILEGENTOOL_DUMP") == "" {
		return
	}
	dir := filepath.Join(os.TempDir(), "filegentool_test")
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Logf("dump %s: %v", name, err)
	} else {
		t.Logf("dumped %s → %s", name, p)
	}
}

// assertPDF validates that PDF bytes are non-empty and structurally valid.
func assertPDF(t *testing.T, name string, pdf []byte) {
	t.Helper()
	if len(pdf) == 0 {
		t.Fatalf("%s: empty PDF output", name)
	}
	if err := validatePDF(pdf); err != nil {
		t.Fatalf("%s: pdfcpu validation failed: %v", name, err)
	}
}

// renderPDF renders markdown to PDF using the test font config.
// Skips the test if the font is not available.
func renderPDF(t *testing.T, md string) []byte {
	t.Helper()
	skipIfNoFont(t)
	pdf, err := renderMarkdownToPDF(md, testFontConfig())
	if err != nil {
		t.Fatalf("render pdf: %v", err)
	}
	return pdf
}

// renderHTML renders markdown to HTML with a default test title.
func renderHTML(t *testing.T, md string) []byte {
	t.Helper()
	html, err := renderMarkdownToHTML(md, "Test Document")
	if err != nil {
		t.Fatalf("render html: %v", err)
	}
	return html
}

// assertHTML validates that HTML bytes are non-empty and contain expected structure.
func assertHTML(t *testing.T, name string, html []byte) {
	t.Helper()
	if len(html) == 0 {
		t.Fatalf("%s: empty HTML output", name)
	}
	s := string(html)
	if !strings.Contains(s, "<!DOCTYPE html>") {
		t.Fatalf("%s: missing DOCTYPE declaration", name)
	}
	if !strings.Contains(s, "<html") {
		t.Fatalf("%s: missing <html> tag", name)
	}
	if !strings.Contains(s, "</html>") {
		t.Fatalf("%s: missing closing </html> tag", name)
	}
	if !strings.Contains(s, "<style>") {
		t.Fatalf("%s: missing <style> block", name)
	}
}

// assertMD validates that Markdown bytes are non-empty.
func assertMD(t *testing.T, name string, md []byte) {
	t.Helper()
	if len(md) == 0 {
		t.Fatalf("%s: empty Markdown output", name)
	}
}

func joinLines(lines []string) string {
	result := ""
	for i, l := range lines {
		if i > 0 {
			result += "\n"
		}
		result += l
	}
	return result
}

// longText repeats a pattern to create text that exceeds one line width.
func longText(words int) string {
	parts := make([]string, words)
	for i := range parts {
		parts[i] = "word"
	}
	return strings.Join(parts, " ")
}

// ============================================================================
// 1. PDF Error & edge-case tests
// ============================================================================

func TestPDFRenderNilFontConfig(t *testing.T) {
	skipIfNoFont(t)
	_, err := renderMarkdownToPDF("hello", nil)
	if err == nil {
		t.Fatal("expected error for nil font config")
	}
	if !strings.Contains(err.Error(), "cjkFontPath") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPDFRenderEmptyCJKFontPath(t *testing.T) {
	skipIfNoFont(t)
	_, err := renderMarkdownToPDF("hello", &FontConfig{CJKFontPath: ""})
	if err == nil {
		t.Fatal("expected error for empty cjkFontPath")
	}
}

func TestPDFRenderEmptyContent(t *testing.T) {
	pdf := renderPDF(t, "")
	assertPDF(t, "empty", pdf)
	dumpFile(t, "empty.pdf", pdf)
}

func TestPDFRenderWhitespaceOnly(t *testing.T) {
	pdf := renderPDF(t, "   \n\n   \n   ")
	assertPDF(t, "whitespace", pdf)
}

func TestPDFRenderSpecialCharacters(t *testing.T) {
	md := "# Special Characters\n\n" +
		"Ampersand & less-than < greater-than >\n\n" +
		"Quotes: \"double\" and 'single'\n\n" +
		"Symbols: @#$%^*()_+-=[]{}|;:',./<>?\n\n" +
		"Backslash: \\\\ and forward slash: /\n\n" +
		"Unicode: café résumé naïf\n"
	pdf := renderPDF(t, md)
	assertPDF(t, "special_chars", pdf)
	dumpFile(t, "special_chars.pdf", pdf)
}

// ============================================================================
// 2. PDF Heading tests
// ============================================================================

func TestPDFRenderAllHeadingLevels(t *testing.T) {
	md := `# Heading One
## Heading Two
### Heading Three
#### Heading Four
##### Heading Five
###### Heading Six`
	pdf := renderPDF(t, md)
	assertPDF(t, "headings", pdf)
	dumpFile(t, "headings.pdf", pdf)
}

func TestPDFRenderH1Centered(t *testing.T) {
	md := "# Centered Title"
	pdf := renderPDF(t, md)
	assertPDF(t, "h1_centered", pdf)
	dumpFile(t, "h1_centered.pdf", pdf)
}

func TestPDFRenderH1LongCenteredWrapping(t *testing.T) {
	md := "# This is a very long document title that should wrap across multiple lines while remaining centered"
	pdf := renderPDF(t, md)
	assertPDF(t, "h1_long_centered", pdf)
	dumpFile(t, "h1_long_centered.pdf", pdf)
}

func TestPDFRenderConsecutiveHeadings(t *testing.T) {
	md := "# Title A\n## Title B\n### Title C\n#### Title D"
	pdf := renderPDF(t, md)
	assertPDF(t, "consecutive_headings", pdf)
}

// ============================================================================
// 3. PDF Paragraph & text wrapping tests
// ============================================================================

func TestPDFRenderParagraph(t *testing.T) {
	md := `This is a simple paragraph with some text.

This is another paragraph with more content to test line wrapping behavior in the PDF renderer when the text exceeds the content width of the page.`
	pdf := renderPDF(t, md)
	assertPDF(t, "paragraphs", pdf)
	dumpFile(t, "paragraphs.pdf", pdf)
}

func TestPDFRenderLongParagraphWrapping(t *testing.T) {
	md := longText(120)
	pdf := renderPDF(t, md)
	assertPDF(t, "long_paragraph", pdf)
	dumpFile(t, "long_paragraph.pdf", pdf)
}

func TestPDFRenderSingleWord(t *testing.T) {
	pdf := renderPDF(t, "Hello")
	assertPDF(t, "single_word", pdf)
}

func TestPDFRenderVeryLongWord(t *testing.T) {
	longWord := strings.Repeat("a", 200)
	md := "# Test\n\n" + longWord
	pdf := renderPDF(t, md)
	assertPDF(t, "very_long_word", pdf)
}

// ============================================================================
// 4. PDF Emphasis tests
// ============================================================================

func TestPDFRenderBold(t *testing.T) {
	md := `This is **bold** text.`
	pdf := renderPDF(t, md)
	assertPDF(t, "bold", pdf)
	dumpFile(t, "bold.pdf", pdf)
}

func TestPDFRenderItalic(t *testing.T) {
	md := `This is *italic* text.`
	pdf := renderPDF(t, md)
	assertPDF(t, "italic", pdf)
}

func TestPDFRenderBoldItalic(t *testing.T) {
	md := `This is ***bold and italic*** text.`
	pdf := renderPDF(t, md)
	assertPDF(t, "bold_italic", pdf)
}

func TestPDFRenderMultipleEmphasis(t *testing.T) {
	md := `This has **bold** and *italic* and ***both*** in one paragraph.`
	pdf := renderPDF(t, md)
	assertPDF(t, "multiple_emphasis", pdf)
}

func TestPDFRenderNestedEmphasis(t *testing.T) {
	md := `This is *italic with **bold inside** text*.`
	pdf := renderPDF(t, md)
	assertPDF(t, "nested_emphasis", pdf)
}

// ============================================================================
// 5. PDF Code tests
// ============================================================================

func TestPDFRenderCodeBlock(t *testing.T) {
	md := "Here is a code block:\n\n```go\nfunc main() {\n    fmt.Println(\"Hello, PDF!\")\n}\n```\n\nAnd some more text."
	pdf := renderPDF(t, md)
	assertPDF(t, "codeblock", pdf)
	dumpFile(t, "codeblock.pdf", pdf)
}

func TestPDFRenderCodeBlockNoLanguage(t *testing.T) {
	md := "```\nsome code\nwithout language\n```"
	pdf := renderPDF(t, md)
	assertPDF(t, "codeblock_no_lang", pdf)
}

func TestPDFRenderCodeBlockEmpty(t *testing.T) {
	md := "```\n```"
	pdf := renderPDF(t, md)
	assertPDF(t, "codeblock_empty", pdf)
}

func TestPDFRenderCodeBlockSingleLine(t *testing.T) {
	md := "```python\nprint('hello')\n```"
	pdf := renderPDF(t, md)
	assertPDF(t, "codeblock_single_line", pdf)
}

func TestPDFRenderCodeBlockMultiPage(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, "// This is line "+string(rune('A'+i%26))+" of the code block that should span multiple pages")
	}
	md := "```go\n" + joinLines(lines) + "\n```"
	pdf := renderPDF(t, md)
	assertPDF(t, "multipage_codeblock", pdf)
	dumpFile(t, "multipage_codeblock.pdf", pdf)
}

func TestPDFRenderInlineCode(t *testing.T) {
	md := "Use the `renderMarkdownToPDF()` function to convert Markdown to PDF."
	pdf := renderPDF(t, md)
	assertPDF(t, "inlinecode", pdf)
	dumpFile(t, "inlinecode.pdf", pdf)
}

func TestPDFRenderMultipleInlineCode(t *testing.T) {
	md := "Use `foo()` and `bar()` together with `baz()`."
	pdf := renderPDF(t, md)
	assertPDF(t, "multiple_inline_code", pdf)
}

func TestPDFRenderInlineCodeLong(t *testing.T) {
	longCode := "very_long_function_name_that_exceeds_the_line_width_and_should_wrap(" + strings.Repeat("arg, ", 20) + "last)"
	md := "Call `" + longCode + "` for details."
	pdf := renderPDF(t, md)
	assertPDF(t, "inlinecode_long", pdf)
}

// ============================================================================
// 6. PDF List tests
// ============================================================================

func TestPDFRenderUnorderedList(t *testing.T) {
	md := `- First item
- Second item
- Third item with a longer description that might wrap to the next line`
	pdf := renderPDF(t, md)
	assertPDF(t, "unordered_list", pdf)
	dumpFile(t, "unordered_list.pdf", pdf)
}

func TestPDFRenderOrderedList(t *testing.T) {
	md := `1. Step one
2. Step two
3. Step three`
	pdf := renderPDF(t, md)
	assertPDF(t, "ordered_list", pdf)
	dumpFile(t, "ordered_list.pdf", pdf)
}

func TestPDFRenderOrderedListManyItems(t *testing.T) {
	var items []string
	for i := 1; i <= 20; i++ {
		items = append(items, fmt.Sprintf("%d. Item number %d", i, i))
	}
	pdf := renderPDF(t, strings.Join(items, "\n"))
	assertPDF(t, "ordered_list_many", pdf)
}

func TestPDFRenderListWithBold(t *testing.T) {
	md := `- **Bold item**: description here
- Normal item: more text
- **Another bold**: with details`
	pdf := renderPDF(t, md)
	assertPDF(t, "list_bold", pdf)
}

func TestPDFRenderListWithInlineCode(t *testing.T) {
	md := "- Use `fmt.Println()` for output\n- Use `log.Fatal()` for errors"
	pdf := renderPDF(t, md)
	assertPDF(t, "list_code", pdf)
}

func TestPDFRenderSingleItemList(t *testing.T) {
	md := "- Only item"
	pdf := renderPDF(t, md)
	assertPDF(t, "single_item_list", pdf)
}

// ============================================================================
// 7. PDF Table tests
// ============================================================================

func TestPDFRenderTable(t *testing.T) {
	md := `| Name  | Age | City     |
|-------|-----|----------|
| Alice | 30  | Beijing  |
| Bob   | 25  | Shanghai |
| Carol | 35  | Shenzhen |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table", pdf)
	dumpFile(t, "table.pdf", pdf)
}

func TestPDFRenderTableSingleColumn(t *testing.T) {
	md := `| Item |
|------|
| A    |
| B    |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_single_col", pdf)
}

func TestPDFRenderTableSingleRow(t *testing.T) {
	md := `| A | B | C |
|---|---|---|
| 1 | 2 | 3 |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_single_row", pdf)
}

func TestPDFRenderTableManyColumns(t *testing.T) {
	md := `| C1 | C2 | C3 | C4 | C5 | C6 | C7 | C8 |
|----|----|----|----|----|----|----|----|
| a  | b  | c  | d  | e  | f  | g  | h  |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_many_cols", pdf)
	dumpFile(t, "table_many_cols.pdf", pdf)
}

func TestPDFRenderTableLongCellWrapping(t *testing.T) {
	md := `| Key | Description |
|-----|-------------|
| A   | This is a very long description that should wrap across multiple lines within the cell |
| B   | Short |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_long_cell", pdf)
	dumpFile(t, "table_long_cell.pdf", pdf)
}

func TestPDFRenderTableEmptyCell(t *testing.T) {
	md := `| A | B | C |
|---|---|---|
| 1 |   | 3 |
|   | 2 |   |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_empty_cell", pdf)
}

func TestPDFRenderTableNumericColumns(t *testing.T) {
	md := `| # | Name           | Value | Description                          |
|---|----------------|-------|--------------------------------------|
| 1 | First item     | 100   | A detailed description of item one   |
| 2 | Second item    | 200   | A detailed description of item two   |
| 3 | Third item     | 300   | A detailed description of item three |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_numeric", pdf)
	dumpFile(t, "table_numeric.pdf", pdf)
}

func TestPDFRenderTableCJKAutoFit(t *testing.T) {
	md := "# 表格自适应宽度测试\n\n" +
		"| 序号 | 公司/品牌 | 代表模型/产品 | 核心优势 |\n" +
		"| :--- | :--- | :--- | :--- |\n" +
		"| 1 | **DeepSeek（深度求索）** | DeepSeek-V3/V4 R1 | 开源策略、极致性价比、推理能力领先 |\n" +
		"| 2 | **字节跳动（豆包）** | 豆包大模型、Seedance 2.0 | C端流量霸主、视频生成差异化 |\n" +
		"| 3 | **阿里巴巴（千问）** | Qwen2.5/Qwen3系列 | 开源生态最丰富、B端云服务协同 |"
	pdf := renderPDF(t, md)
	assertPDF(t, "table_cjk_autofit", pdf)
	dumpFile(t, "table_autofit.pdf", pdf)
}

func TestCollectTableRowsHeader(t *testing.T) {
	md := "| 层级 | 代表企业 | 市场份额（估算） |\n" +
		"|------|----------|:---:|\n" +
		"| 第一梯队 | 字节、阿里、百度、腾讯、华为 | ~65% |\n" +
		"| 第二梯队 | DeepSeek、智谱、月之暗面、百川 | ~20% |\n" +
		"| 其他 | 面壁、零一万物、阶跃星辰等 | ~15% |"

	source := []byte(md)
	gm := goldmark.New(goldmark.WithExtensions(extension.NewTable()))
	doc := gm.Parser().Parse(gmText.NewReader(source))

	var tableNode *extAst.Table
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if tbl, ok := n.(*extAst.Table); ok {
				tableNode = tbl
				return ast.WalkStop, nil
			}
		}
		return ast.WalkContinue, nil
	})
	if tableNode == nil {
		t.Fatal("no Table node found in parsed markdown")
	}

	firstChild := tableNode.FirstChild()
	if firstChild == nil {
		t.Fatal("Table has no children")
	}
	if _, ok := firstChild.(*extAst.TableHeader); !ok {
		t.Fatalf("Table's first child should be *TableHeader, got %T", firstChild)
	}

	childCount := 0
	headerCount := 0
	dataRowCount := 0
	for child := tableNode.FirstChild(); child != nil; child = child.NextSibling() {
		childCount++
		switch child.(type) {
		case *extAst.TableHeader:
			headerCount++
		case *extAst.TableRow:
			dataRowCount++
		}
	}
	if headerCount != 1 {
		t.Errorf("expected 1 TableHeader, got %d", headerCount)
	}
	if dataRowCount != 3 {
		t.Errorf("expected 3 TableRow (data), got %d", dataRowCount)
	}
	if childCount != 4 {
		t.Errorf("expected 4 total children, got %d", childCount)
	}

	pdf := renderPDF(t, md)
	assertPDF(t, "table_header_regression", pdf)
	t.Logf("PDF size: %d bytes", len(pdf))
}

// ============================================================================
// 8. PDF Thematic break & link tests
// ============================================================================

func TestPDFRenderThematicBreak(t *testing.T) {
	md := `First section.

---

Second section.`
	pdf := renderPDF(t, md)
	assertPDF(t, "thematicbreak", pdf)
	dumpFile(t, "thematicbreak.pdf", pdf)
}

func TestPDFRenderMultipleThematicBreaks(t *testing.T) {
	md := "Section A\n\n---\n\nSection B\n\n---\n\nSection C"
	pdf := renderPDF(t, md)
	assertPDF(t, "multiple_breaks", pdf)
}

func TestPDFRenderLink(t *testing.T) {
	md := `Visit [Google](https://google.com) for more info.`
	pdf := renderPDF(t, md)
	assertPDF(t, "link", pdf)
	dumpFile(t, "link.pdf", pdf)
}

func TestPDFRenderMultipleLinks(t *testing.T) {
	md := "Check [Google](https://google.com) and [GitHub](https://github.com) for details."
	pdf := renderPDF(t, md)
	assertPDF(t, "multiple_links", pdf)
}

// ============================================================================
// 9. PDF CJK tests
// ============================================================================

func TestPDFRenderChineseBasic(t *testing.T) {
	md := "# 中文测试\n\n这是一段中文内容，包含**加粗**和*斜体*。\n\n- 第一项\n- 第二项\n\n| 名称 | 数量 |\n|------|------|\n| 苹果 | 10 |\n| 香蕉 | 20 |"
	pdf := renderPDF(t, md)
	assertPDF(t, "chinese_basic", pdf)
	dumpFile(t, "chinese_basic.pdf", pdf)
}

func TestPDFRenderChineseComprehensive(t *testing.T) {
	md := "# 项目报告\n\n" +
		"## 概述\n\n" +
		"这是一份**综合**测试文档，验证*中文*渲染效果。\n\n" +
		"## 功能列表\n\n" +
		"- 标题渲染\n" +
		"- **加粗**和*斜体*文本\n" +
		"- 代码块支持\n\n" +
		"### 代码示例\n\n" +
		"```go\nfmt.Println(\"你好PDF\")\n```\n\n" +
		"### 数据表格\n\n" +
		"| 指标   | 数值  | 状态 |\n|--------|-------|------|\n| 用户数 | 1200 | 正常 |\n| 收入   | 50万  | 良好 |\n| 缺陷   | 3     | 低   |\n\n" +
		"---\n\n" +
		"## 结论\n\n" +
		"中文渲染测试完成。\n"

	pdf := renderPDF(t, md)
	assertPDF(t, "chinese_comprehensive", pdf)
	dumpFile(t, "chinese_comprehensive.pdf", pdf)
}

func TestPDFRenderMixedLatinChinese(t *testing.T) {
	md := "# Mixed Language Test\n\n" +
		"This paragraph contains both English and 中文 content.\n\n" +
		"项目名称：Maestro Agent Platform\n\n" +
		"- Feature A: 支持多语言\n" +
		"- Feature B: PDF generation\n" +
		"- Feature C: 自动化流程\n\n" +
		"| English | 中文 | Number |\n|---------|------|--------|\n| Hello   | 你好 | 123    |\n| World   | 世界 | 456    |"

	pdf := renderPDF(t, md)
	assertPDF(t, "mixed_latin_chinese", pdf)
	dumpFile(t, "mixed_latin_chinese.pdf", pdf)
}

func TestPDFRenderCJKLongParagraph(t *testing.T) {
	md := "# 中文长段落换行测试\n\n" +
		"2023年初ChatGPT引爆全球AI热潮后，中国迅速掀起了「百模大战」——据不完全统计，截至2023年底，国内累计发布的大模型数量超过200个，涵盖通用大模型、行业大模型和垂直场景模型。这场竞赛被业界形象地称为「百模大战」。\n\n" +
		"然而，进入2025至2026年，「百模大战」的格局已发生根本性转变。根据2026年政府工作报告，国家明确提出「加快发展新质生产力」「推动人工智能高质量发展」，政策导向从鼓励数量转向注重质量。行业从「谁有大模型」的跑马圈地阶段，全面进入「谁的大模型真正有用」的淘汰赛阶段。\n\n" +
		"据统计，2023年国内发布过自研大模型的企业超过200家，但截至2026年中约60%的厂商已停止大模型独立研发，转向调用头部厂商API约25%转型为AI应用开发商，放弃底层模型自研约10%彻底退出AI大模型赛道仅5%左右仍在坚持自研大模型并保持竞争力。"

	pdf := renderPDF(t, md)
	assertPDF(t, "cjk_long_paragraph", pdf)
	dumpFile(t, "cjk_long_paragraph.pdf", pdf)
}

func TestPDFRenderCJKHeadingCentered(t *testing.T) {
	md := "# 2026年AI行业「百模大战」现状深度分析报告：从百花齐放到五强争霸的终局判断\n\n## 二级标题应左对齐\n\n正文段落。"
	pdf := renderPDF(t, md)
	assertPDF(t, "cjk_heading_centered", pdf)
	dumpFile(t, "heading_centered.pdf", pdf)
}

func TestPDFRenderJapanese(t *testing.T) {
	md := "# 日本語テスト\n\nこれは日本語のテストです。ひらがなとカタカナと漢字を含みます。\n\n- ひらがな：あいうえお\n- カタカナ：アイウエオ\n- 漢字：日本語"
	pdf := renderPDF(t, md)
	assertPDF(t, "japanese", pdf)
	dumpFile(t, "japanese.pdf", pdf)
}

func TestPDFRenderKorean(t *testing.T) {
	md := "# 한국어 테스트\n\n이것은 한국어 테스트입니다. 한글을 포함합니다."
	pdf := renderPDF(t, md)
	assertPDF(t, "korean", pdf)
	dumpFile(t, "korean.pdf", pdf)
}

// ============================================================================
// 10. containsCJK unit tests
// ============================================================================

func TestContainsCJK(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Non-CJK
		{"Hello World", false},
		{"12345", false},
		{"café résumé naïf", false},
		{"", false},
		{"!@#$%^&*()", false},

		// CJK variants
		{"你好世界", true},     // Han
		{"Hello 世界", true}, // Mixed
		{"カタカナ", true},     // Katakana
		{"ひらがな", true},     // Hiragana
		{"한글", true},       // Hangul
		{"สวัสดี", true},   // Thai
		{"مرحبا", true},    // Arabic
		{"नमस्ते", true},   // Devanagari
		{"项目报告", true},     // Han (simplified)

		// Edge: single CJK character
		{"中", true},
		{"あ", true},
		{"한", true},
	}
	for _, tt := range tests {
		got := containsCJK(tt.input)
		if got != tt.want {
			t.Errorf("containsCJK(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ============================================================================
// 10b. Emoji stripping unit tests
// ============================================================================

func TestIsEmojiRune(t *testing.T) {
	tests := []struct {
		r    rune
		want bool
	}{
		// Emoji
		{'🚀', true},  // U+1F680 Transport
		{'🎉', true},  // U+1F389 Party popper
		{'✅', true},  // U+2705 Check mark button
		{'❌', true},  // U+274C Cross mark
		{'❤', true},  // U+2764 Heavy black heart
		{'🔥', true}, // U+1F525 Fire
		{'📊', true}, // U+1F4CA Bar chart
		{'⭐', true}, // U+2B50 Star
		{'❗', true}, // U+2757 Exclamation mark
		{'‼', true},  // U+203C Double exclamation
		{'©', false}, // U+00A9 — not emoji
		{'®', false}, // U+00AE — not emoji
		{'中', false}, // Han CJK
		{'A', false},  // Latin
		{'0', false},  // Digit
		{' ', false},  // Space
		{'+', false},  // Symbol
	}
	for _, tt := range tests {
		got := isEmojiRune(tt.r)
		if got != tt.want {
			t.Errorf("isEmojiRune(%q U+%04X) = %v, want %v", string(tt.r), tt.r, got, tt.want)
		}
	}
}

func TestStripEmoji(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// No emoji — returned as-is
		{"Hello World", "Hello World"},
		{"中文测试", "中文测试"},

		// Emoji stripped
		{"Hello 🚀 World", "Hello  World"},
		{"✅ Done ❌ Failed", " Done  Failed"},
		{"🎉🎊 Party 🎈", " Party "},

		// Mixed CJK + emoji
		{"中文 ✅ 通过", "中文  通过"},
		{"任务完成 🚀🔥", "任务完成 "},

		// Only emoji
		{"🚀🎉✅", ""},

		// Empty
		{"", ""},

		// Emoji at boundaries
		{"🚀Leading", "Leading"},
		{"Trailing🎉", "Trailing"},
	}
	for _, tt := range tests {
		got := stripEmoji(tt.input)
		if got != tt.want {
			t.Errorf("stripEmoji(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPDFRenderEmojiStripped(t *testing.T) {
	// Emoji should be stripped so no blank gaps appear
	md := "# Emoji Test\n\nHello world 🚀 🎉 ✅ ❌"
	pdf := renderPDF(t, md)
	assertPDF(t, "emoji_stripped", pdf)
	dumpFile(t, "emoji_stripped.pdf", pdf)
}

func TestPDFRenderCJKWithEmoji(t *testing.T) {
	md := "# 混合测试\n\n" +
		"- 项目A：支持中文 ✅\n" +
		"- 项目B：支持表格 ✅\n\n" +
		"| 功能 | 状态 |\n|------|------|\n| 中文渲染 | ✅ |\n| 表格渲染 | ✅ |"
	pdf := renderPDF(t, md)
	assertPDF(t, "cjk_with_emoji", pdf)
	dumpFile(t, "cjk_with_emoji.pdf", pdf)
}

// ============================================================================
// 11. PDF Comprehensive / stress tests
// ============================================================================

func TestPDFRenderComprehensive(t *testing.T) {
	md := "# Project Report\n\n" +
		"## Overview\n\n" +
		"This is a **comprehensive** document that tests *all* Markdown elements in a single PDF.\n\n" +
		"## Features\n\n" +
		"- Heading rendering with multiple levels\n" +
		"- **Bold** and *italic* text\n" +
		"- `Inline code` support\n" +
		"- Ordered and unordered lists\n\n" +
		"### Code Example\n\n" +
		"```python\ndef hello():\n    print(\"Hello from PDF!\")\n```\n\n" +
		"### Data Table\n\n" +
		"| Metric   | Value | Status |\n|----------|-------|--------|\n| Users    | 1,200 | OK     |\n| Revenue  | $50K  | Good   |\n| Bugs     | 3     | Low    |\n\n" +
		"---\n\n" +
		"## Conclusion\n\n" +
		"Everything works as expected. See the [documentation](https://example.com) for details.\n"
	pdf := renderPDF(t, md)
	assertPDF(t, "comprehensive", pdf)
	dumpFile(t, "comprehensive.pdf", pdf)
}

func TestPDFRenderComprehensiveCJK(t *testing.T) {
	md := "# 综合测试报告\n\n" +
		"## 概述\n\n" +
		"本报告验证 PDF 工具对**中文**文档的完整渲染能力，包括*斜体*、`行内代码`和表格。\n\n" +
		"## 功能清单\n\n" +
		"1. 标题层级（H1-H6）\n" +
		"2. **加粗**与*斜体*\n" +
		"3. 代码块\n" +
		"4. 表格\n\n" +
		"### 代码示例\n\n" +
		"```go\nfmt.Println(\"你好\")\n```\n\n" +
		"### 数据表\n\n" +
		"| 指标 | 数值 | 状态 |\n|------|------|------|\n| UV   | 1.2万 | 正常 |\n| 错误率 | 0.3% | 低 |\n\n" +
		"---\n\n" +
		"## 外部链接\n\n" +
		"访问 [官方文档](https://example.com) 了解更多。\n\n" +
		"## 结论\n\n" +
		"中文 PDF 渲染功能正常。\n"

	pdf := renderPDF(t, md)
	assertPDF(t, "comprehensive_cjk", pdf)
	dumpFile(t, "comprehensive_cjk.pdf", pdf)
}

func TestPDFRenderManyPages(t *testing.T) {
	var body strings.Builder
	body.WriteString("# Multi-Page Document\n\n")
	for i := 0; i < 30; i++ {
		body.WriteString(fmt.Sprintf("## Section %d\n\n", i+1))
		body.WriteString(longText(80) + "\n\n")
	}
	pdf := renderPDF(t, body.String())
	assertPDF(t, "many_pages", pdf)
	if len(pdf) < 10000 {
		t.Fatalf("expected multi-page PDF to be large, got %d bytes", len(pdf))
	}
	dumpFile(t, "many_pages.pdf", pdf)
}

func TestPDFRenderEmoji(t *testing.T) {
	md := "# Emoji Test\n\nHello world 🚀 🎉 ✅ ❌"
	pdf := renderPDF(t, md)
	assertPDF(t, "emoji", pdf)
	dumpFile(t, "emoji.pdf", pdf)
}

func TestPDFRenderValidatePDF(t *testing.T) {
	skipIfNoFont(t)
	md := `# Validation Test
This PDF should pass pdfcpu validation.`
	pdfBytes, err := renderMarkdownToPDF(md, testFontConfig())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := validatePDF(pdfBytes); err != nil {
		t.Fatalf("pdfcpu validation failed: %v", err)
	}
}

// ============================================================================
// 12. PDF Mixed-element combination tests
// ============================================================================

func TestPDFRenderTableAfterList(t *testing.T) {
	md := "- Item one\n- Item two\n\n| A | B |\n|---|---|\n| 1 | 2 |"
	pdf := renderPDF(t, md)
	assertPDF(t, "table_after_list", pdf)
}

func TestPDFRenderCodeBlockBetweenParagraphs(t *testing.T) {
	md := "Before code.\n\n```go\nfmt.Println(\"hello\")\n```\n\nAfter code."
	pdf := renderPDF(t, md)
	assertPDF(t, "code_between_paragraphs", pdf)
}

func TestPDFRenderHeadingAfterCodeBlock(t *testing.T) {
	md := "```go\ncode\n```\n\n## Heading After Code"
	pdf := renderPDF(t, md)
	assertPDF(t, "heading_after_code", pdf)
}

func TestPDFRenderTableAfterCodeBlock(t *testing.T) {
	md := "```js\nconsole.log('hi')\n```\n\n| Col1 | Col2 |\n|------|------|\n| A    | B    |"
	pdf := renderPDF(t, md)
	assertPDF(t, "table_after_code", pdf)
}

func TestPDFRenderLinkInParagraph(t *testing.T) {
	md := "For more info, visit the [documentation](https://example.com) and read carefully."
	pdf := renderPDF(t, md)
	assertPDF(t, "link_in_paragraph", pdf)
}

func TestPDFRenderLinkAfterTable(t *testing.T) {
	md := "| X | Y |\n|---|---|\n| 1 | 2 |\n\nSee [reference](https://example.com)."
	pdf := renderPDF(t, md)
	assertPDF(t, "link_after_table", pdf)
}

func TestPDFRenderInlineCodeInList(t *testing.T) {
	md := "- Use `go test` to run tests\n- Use `go build` to compile\n- Check the [docs](https://go.dev) for help"
	pdf := renderPDF(t, md)
	assertPDF(t, "inline_code_in_list", pdf)
}

func TestPDFRenderCJKWithCodeBlock(t *testing.T) {
	md := "# 中文代码示例\n\n以下是一段 Go 代码：\n\n```go\n// 你好世界\nfmt.Println(\"你好\")\n```\n\n代码执行完毕。"
	pdf := renderPDF(t, md)
	assertPDF(t, "cjk_with_codeblock", pdf)
}

func TestPDFRenderCJKWithInlineCode(t *testing.T) {
	md := "使用 `fmt.Println()` 函数输出\"你好\"信息。"
	pdf := renderPDF(t, md)
	assertPDF(t, "cjk_with_inline_code", pdf)
}

func TestPDFRenderCJKMixedListTable(t *testing.T) {
	md := "# 混合测试\n\n" +
		"- 项目A：支持中文\n" +
		"- 项目B：支持表格\n\n" +
		"| 功能 | 状态 |\n|------|------|\n| 中文渲染 | ✅ |\n| 表格渲染 | ✅ |"
	pdf := renderPDF(t, md)
	assertPDF(t, "cjk_mixed_list_table", pdf)
}

// ============================================================================
// 13. PDF Table edge cases
// ============================================================================

func TestPDFRenderTableWithBoldHeaders(t *testing.T) {
	md := `| **Category** | **Count** |
|-------------|-----------|
| Fruit       | 5         |
| Vegetable   | 3         |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_bold_headers", pdf)
}

func TestPDFRenderTableInconsistentColumns(t *testing.T) {
	md := `| A | B | C |
|---|---|---|
| 1 | 2 | 3 |
| 4 | 5 |   |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_inconsistent", pdf)
}

func TestPDFRenderTableCJKLongCell(t *testing.T) {
	md := "| 类型 | 说明 |\n|------|------|\n| 测试 | 这是一段很长的中文描述文本，用来验证表格单元格是否能正确换行而不会溢出边界，需要确保文字在列宽限制内自动折行 |\n| 其他 | 短文本 |"
	pdf := renderPDF(t, md)
	assertPDF(t, "table_cjk_long_cell", pdf)
	dumpFile(t, "table_cjk_long_cell.pdf", pdf)
}

func TestPDFRenderTableAllNumeric(t *testing.T) {
	md := `| Q1  | Q2  | Q3  | Q4  |
|-----|-----|-----|-----|
| 100 | 200 | 150 | 300 |
| 110 | 210 | 160 | 310 |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_all_numeric", pdf)
}

func TestPDFRenderTableTwoColumnsWide(t *testing.T) {
	md := `| Title | Description |
|-------|-------------|
| Alpha | This is a moderately long description for the first item in the table |
| Beta  | This is another description that is also quite long and should wrap nicely |`
	pdf := renderPDF(t, md)
	assertPDF(t, "table_two_wide_cols", pdf)
}

// ============================================================================
// 14. HTML rendering tests
// ============================================================================

func TestHTMLRenderHeading(t *testing.T) {
	md := "# Hello World\n## Subtitle\n### Section"
	html := renderHTML(t, md)
	assertHTML(t, "heading", html)
	if !strings.Contains(string(html), "<h1>") {
		t.Fatal("expected <h1> tag in HTML output")
	}
	if !strings.Contains(string(html), "<h2>") {
		t.Fatal("expected <h2> tag in HTML output")
	}
	dumpFile(t, "heading.html", html)
}

func TestHTMLRenderParagraph(t *testing.T) {
	md := "This is a paragraph with some text."
	html := renderHTML(t, md)
	assertHTML(t, "paragraph", html)
	if !strings.Contains(string(html), "<p>") {
		t.Fatal("expected <p> tag in HTML output")
	}
	dumpFile(t, "paragraph.html", html)
}

func TestHTMLRenderBold(t *testing.T) {
	md := "This is **bold** text."
	html := renderHTML(t, md)
	assertHTML(t, "bold", html)
	if !strings.Contains(string(html), "<strong>") {
		t.Fatal("expected <strong> tag in HTML output")
	}
}

func TestHTMLRenderItalic(t *testing.T) {
	md := "This is *italic* text."
	html := renderHTML(t, md)
	assertHTML(t, "italic", html)
	if !strings.Contains(string(html), "<em>") {
		t.Fatal("expected <em> tag in HTML output")
	}
}

func TestHTMLRenderCodeBlock(t *testing.T) {
	md := "```go\nfmt.Println(\"hello\")\n```"
	html := renderHTML(t, md)
	assertHTML(t, "codeblock", html)
	if !strings.Contains(string(html), "<code") {
		t.Fatal("expected <code> tag in HTML output")
	}
	if !strings.Contains(string(html), "<pre>") {
		t.Fatal("expected <pre> tag in HTML output")
	}
	dumpFile(t, "codeblock.html", html)
}

func TestHTMLRenderInlineCode(t *testing.T) {
	md := "Use the `renderMarkdownToHTML()` function."
	html := renderHTML(t, md)
	assertHTML(t, "inlinecode", html)
	if !strings.Contains(string(html), "<code>") {
		t.Fatal("expected <code> tag in HTML output")
	}
}

func TestHTMLRenderUnorderedList(t *testing.T) {
	md := "- Item one\n- Item two\n- Item three"
	html := renderHTML(t, md)
	assertHTML(t, "unordered_list", html)
	if !strings.Contains(string(html), "<ul>") {
		t.Fatal("expected <ul> tag in HTML output")
	}
	if !strings.Contains(string(html), "<li>") {
		t.Fatal("expected <li> tag in HTML output")
	}
	dumpFile(t, "unordered_list.html", html)
}

func TestHTMLRenderOrderedList(t *testing.T) {
	md := "1. Step one\n2. Step two\n3. Step three"
	html := renderHTML(t, md)
	assertHTML(t, "ordered_list", html)
	if !strings.Contains(string(html), "<ol>") {
		t.Fatal("expected <ol> tag in HTML output")
	}
	dumpFile(t, "ordered_list.html", html)
}

func TestHTMLRenderTable(t *testing.T) {
	md := `| Name  | Age | City     |
|-------|-----|----------|
| Alice | 30  | Beijing  |
| Bob   | 25  | Shanghai |`
	html := renderHTML(t, md)
	assertHTML(t, "table", html)
	if !strings.Contains(string(html), "<table>") {
		t.Fatal("expected <table> tag in HTML output")
	}
	if !strings.Contains(string(html), "<th>") {
		t.Fatal("expected <th> tag in HTML output")
	}
	if !strings.Contains(string(html), "<td>") {
		t.Fatal("expected <td> tag in HTML output")
	}
	dumpFile(t, "table.html", html)
}

func TestHTMLRenderLink(t *testing.T) {
	md := "Visit [Google](https://google.com) for more info."
	html := renderHTML(t, md)
	assertHTML(t, "link", html)
	if !strings.Contains(string(html), `<a href="https://google.com">`) {
		t.Fatal("expected <a> tag with href in HTML output")
	}
}

func TestHTMLRenderThematicBreak(t *testing.T) {
	md := "Section A\n\n---\n\nSection B"
	html := renderHTML(t, md)
	assertHTML(t, "thematicbreak", html)
	if !strings.Contains(string(html), "<hr") {
		t.Fatal("expected <hr> tag in HTML output")
	}
}

func TestHTMLRenderCJK(t *testing.T) {
	md := "# 中文测试\n\n这是一段中文内容，包含**加粗**和*斜体*。\n\n- 第一项\n- 第二项"
	html := renderHTML(t, md)
	assertHTML(t, "cjk", html)
	if !strings.Contains(string(html), "中文测试") {
		t.Fatal("expected CJK characters in HTML output")
	}
	dumpFile(t, "cjk.html", html)
}

func TestHTMLRenderComprehensive(t *testing.T) {
	md := "# Project Report\n\n" +
		"## Overview\n\n" +
		"This is a **comprehensive** document that tests *all* Markdown elements.\n\n" +
		"## Features\n\n" +
		"- Heading rendering\n" +
		"- **Bold** and *italic* text\n" +
		"- `Inline code` support\n\n" +
		"### Code Example\n\n" +
		"```python\ndef hello():\n    print(\"Hello from HTML!\")\n```\n\n" +
		"### Data Table\n\n" +
		"| Metric  | Value | Status |\n|---------|-------|--------|\n| Users   | 1,200 | OK     |\n| Revenue | $50K  | Good   |\n\n" +
		"---\n\n" +
		"## Conclusion\n\n" +
		"See the [documentation](https://example.com) for details.\n"

	html := renderHTML(t, md)
	assertHTML(t, "comprehensive", html)
	dumpFile(t, "comprehensive.html", html)
}

func TestHTMLRenderEmptyContent(t *testing.T) {
	html := renderHTML(t, "")
	assertHTML(t, "empty", html)
}

func TestHTMLRenderSpecialCharacters(t *testing.T) {
	md := "Ampersand & less-than < greater-than >\n\nQuotes: \"double\" and 'single'"
	html := renderHTML(t, md)
	assertHTML(t, "special_chars", html)
	dumpFile(t, "special_chars.html", html)
}

func TestHTMLDocumentStructure(t *testing.T) {
	md := "# Test"
	html := renderHTML(t, md)
	s := string(html)

	// Verify complete HTML5 document structure
	if !strings.HasPrefix(s, "<!DOCTYPE html>") {
		t.Fatal("expected DOCTYPE declaration at start")
	}
	if !strings.Contains(s, `<meta charset="UTF-8">`) {
		t.Fatal("expected UTF-8 charset meta tag")
	}
	if !strings.Contains(s, "<style>") {
		t.Fatal("expected <style> block for CSS")
	}
	if !strings.Contains(s, "</style>") {
		t.Fatal("expected closing </style> tag")
	}
	if !strings.Contains(s, "</body>") {
		t.Fatal("expected closing </body> tag")
	}
}

func TestHTMLTitleFromFilename(t *testing.T) {
	tests := []struct {
		filename string
		wantTitle string
	}{
		{"report.html", "report"},
		{"analysis.htm", "analysis"},
		{"my-doc.html", "my-doc"},
		{"2026年度报告.html", "2026年度报告"},
		{"deep/nested/path.html", "deep/nested/path"}, // only ext stripped, not path
	}
	for _, tt := range tests {
		title := strings.TrimSuffix(tt.filename, filepath.Ext(tt.filename))
		if title != tt.wantTitle {
			t.Errorf("TrimSuffix(%q) = %q, want %q", tt.filename, title, tt.wantTitle)
		}
	}

	// Verify the title actually appears in the rendered HTML
	md := "# Test"
	html, err := renderMarkdownToHTML(md, "report")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(html), "<title>report</title>") {
		t.Fatalf("expected <title>report</title> in HTML, got:\n%s", string(html))
	}
}

// ============================================================================
// 15. Markdown (direct save) tests
// ============================================================================

func TestMDRenderSimple(t *testing.T) {
	md := "# Hello World\n\nThis is a paragraph."
	data := []byte(md)
	assertMD(t, "simple", data)
	if string(data) != md {
		t.Fatal("markdown content should be preserved as-is")
	}
}

func TestMDRenderCJK(t *testing.T) {
	md := "# 中文测试\n\n这是一段中文内容。"
	data := []byte(md)
	assertMD(t, "cjk", data)
	if !strings.Contains(string(data), "中文") {
		t.Fatal("expected CJK characters in markdown output")
	}
}

func TestMDRenderCodeBlock(t *testing.T) {
	md := "```go\nfmt.Println(\"hello\")\n```"
	data := []byte(md)
	assertMD(t, "codeblock", data)
	if !strings.Contains(string(data), "fmt.Println") {
		t.Fatal("expected code content in markdown output")
	}
}

func TestMDRenderTable(t *testing.T) {
	md := "| Name | Age |\n|------|-----|\n| Alice | 30 |"
	data := []byte(md)
	assertMD(t, "table", data)
	if !strings.Contains(string(data), "Alice") {
		t.Fatal("expected table content in markdown output")
	}
}

func TestMDRenderComprehensive(t *testing.T) {
	md := "# Project Report\n\n" +
		"## Overview\n\n" +
		"This is a **comprehensive** document.\n\n" +
		"- Item one\n" +
		"- Item two\n\n" +
		"| Metric | Value |\n|--------|-------|\n| Users  | 1,200 |\n"

	data := []byte(md)
	assertMD(t, "comprehensive", data)
	// Markdown should be preserved verbatim
	if string(data) != md {
		t.Fatal("markdown content should be preserved as-is")
	}
}

// ============================================================================
// 16. Cross-format consistency tests
// ============================================================================

func TestCrossFormatSameContent(t *testing.T) {
	// The same markdown content should be renderable to all three formats
	md := "# Test Document\n\nThis is a **test** with *emphasis* and `code`.\n\n" +
		"- Item one\n- Item two\n\n" +
		"| A | B |\n|---|---|\n| 1 | 2 |"

	// PDF
	pdf := renderPDF(t, md)
	assertPDF(t, "cross_pdf", pdf)

	// HTML
	html := renderHTML(t, md)
	assertHTML(t, "cross_html", html)

	// MD
	mdData := []byte(md)
	assertMD(t, "cross_md", mdData)
}

func TestCrossFormatCJK(t *testing.T) {
	md := "# 中文测试\n\n这是一段中文内容，包含**加粗**和*斜体*。\n\n| 名称 | 数量 |\n|------|------|\n| 苹果 | 10 |"

	// PDF
	pdf := renderPDF(t, md)
	assertPDF(t, "cross_cjk_pdf", pdf)

	// HTML
	html := renderHTML(t, md)
	assertHTML(t, "cross_cjk_html", html)
	if !strings.Contains(string(html), "中文") {
		t.Fatal("expected CJK characters in HTML output")
	}

	// MD
	mdData := []byte(md)
	assertMD(t, "cross_cjk_md", mdData)
}

func TestLooksLikeHTMLDocument(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "plain markdown",
			content: "# Hello\n\nSome **bold** text and a [link](https://example.com).",
			want:    false,
		},
		{
			name:    "markdown with inline html tag",
			content: "# Title\n\nUse `<div>` for layout.",
			want:    false,
		},
		{
			name:    "literal html document",
			content: "<!DOCTYPE html>\n<html><head><style>body{}</style></head><body><h1>Hi</h1></body></html>",
			want:    true,
		},
		{
			name:    "html with only body tag",
			content: "<body>\n<h1>Title</h1>\n</body>",
			want:    true,
		},
		{
			name:    "escaped unicode \\u003chtml",
			content: "\\u003c!DOCTYPE html\\u003e\n\\u003chtml\\u003e\\u003chead\\u003e\\u003cstyle\\u003ebody{}\\u003c/style\\u003e\\u003c/head\\u003e\\u003cbody\\u003e\\u003ch1\\u003eHi\\u003c/h1\\u003e\\u003c/body\\u003e\\u003c/html\\u003e",
			want:    true,
		},
		{
			name:    "escaped unicode \\u003cstyle only",
			content: "\\u003cstyle\\u003e* { margin: 0; }\\u003c/style\\u003e\n# Title",
			want:    true,
		},
		{
			name:    "escaped unicode \\u003cscript",
			content: "\\u003cscript\\u003ealert(1)\\u003c/script\\u003e",
			want:    true,
		},
		{
			name:    "uppercase HTML tags",
			content: "<HTML><HEAD></HEAD><BODY></BODY></HTML>",
			want:    true,
		},
		{
			name:    "mixed case escaped",
			content: `<HTML><BODY>Hello</BODY></HTML>`,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeHTMLDocument(tt.content)
			if got != tt.want {
				t.Errorf("looksLikeHTMLDocument(%q) = %v, want %v", tt.content[:min(len(tt.content), 60)], got, tt.want)
			}
		})
	}
}
