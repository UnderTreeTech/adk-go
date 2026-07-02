package filegentool

import (
	"fmt"
	"path/filepath"
	"strings"

	at "github.com/UnderTreeTech/adk-go/artifact"
	toolutils "github.com/UnderTreeTech/adk-go/tools"

	"github.com/google/uuid"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

// Args defines the arguments for the file generation tool.
// Only .pdf, .html/.htm, and .md formats are supported.
// For HTML and PDF, the Content field is treated as Markdown and rendered
// to the target format before saving. For .md the content is saved as-is
// (with code-block markers cleaned).
type Args struct {
	FileName string `json:"filename" description:"要创建的文件名。仅支持 .pdf、.html 和 .md 三种格式，例如 'report.pdf'、'report.html'、'report.md'。"`
	Content  string `json:"content" description:"文档正文，必须是纯 Markdown 文本（使用 #、##、-、**加粗**、> 引用、围栏代码块、表格等 Markdown 语法）。本工具会在内部自动将 Markdown 渲染为目标格式，即使生成 .html 或 .pdf 也必须传入 Markdown，禁止传入 HTML/CSS 源码。不允许包含 <!DOCTYPE>、<html>、<head>、<body>、<div>、<style>、<script> 等任何 HTML 标签。正确示例：'# 标题（换行）## 第一章（换行）从前有一片森林……'。错误示例（会被拒绝）：'<html><body><h1>标题</h1>...</html>'。"`
}

// New creates a new file generation tool instance that supports three output
// formats only: Markdown (saved as-is), HTML (rendered from Markdown), and PDF
// (rendered from Markdown with CJK support). Other formats are rejected.
//
// Parameters:
//   - svc: artifact service for persisting generated files
//   - cfg: artifact configuration for generating file URLs
//   - fc: font configuration for PDF rendering. Must be non-nil and
//     CJKFontPath must be non-empty; otherwise New returns an error.
func New(svc artifact.Service, cfg *at.Config, fc *FontConfig) (tool.Tool, error) {
	if fc == nil {
		return nil, fmt.Errorf("filegentool: FontConfig must not be nil")
	}
	if fc.CJKFontPath == "" {
		return nil, fmt.Errorf("filegentool: FontConfig.CJKFontPath must not be empty")
	}

	return functiontool.New(functiontool.Config{
		Name:        "generate_file",
		Description: "生成文档文件并保存到存储服务器。仅支持三种格式：Markdown (.md)、HTML (.html/.htm) 和 PDF (.pdf)。重要：'content' 参数必须始终为纯 Markdown 文本，本工具会在内部自动完成 Markdown 到 HTML 及 Markdown 到 PDF 的渲染，因此绝对不能传入原始 HTML/CSS（不允许包含 <!DOCTYPE>、<html>、<style>、<script> 等标签），传入 HTML 会被拒绝。对于 .md 格式，Markdown 内容将原样保存。不要将此工具用于其他文件类型（如 .docx、.xlsx、.pptx、.csv、.json、.xml、代码文件等），这些格式不受支持且会失败。",
	}, func(ctx tool.Context, args Args) (map[string]any, error) {
		if args.FileName == "" {
			return nil, fmt.Errorf("filename is required")
		}
		if args.Content == "" {
			return nil, fmt.Errorf("content is required")
		}

		fileExt := strings.ToLower(filepath.Ext(args.FileName))

		var data []byte
		var mimeType string

		switch fileExt {
		case ".pdf":
			// Render Markdown → PDF
			pdfBytes, err := renderMarkdownToPDF(args.Content, fc)
			if err != nil {
				return nil, fmt.Errorf("render markdown to pdf: %w", err)
			}
			// Validate PDF with pdfcpu
			if err := validatePDF(pdfBytes); err != nil {
				return nil, fmt.Errorf("validate pdf: %w", err)
			}
			data = pdfBytes
			mimeType = "application/pdf"

		case ".html", ".htm":
			// content must be Markdown (rendered here); reject raw HTML docs.
			if looksLikeHTMLDocument(args.Content) {
				return nil, fmt.Errorf("content must be Markdown, not HTML: this tool renders Markdown to HTML itself; please resend 'content' as plain Markdown without <!DOCTYPE>/<html>/<head>/<body>/<style>/<script> tags")
			}
			// Render Markdown → HTML, use filename (without extension) as <title>
			title := strings.TrimSuffix(args.FileName, filepath.Ext(args.FileName))
			htmlBytes, err := renderMarkdownToHTML(args.Content, title)
			if err != nil {
				return nil, fmt.Errorf("render markdown to html: %w", err)
			}
			data = htmlBytes
			mimeType = "text/html"

		case ".md":
			// Clean markdown code block markers if present
			cleaned := toolutils.CleanMarkdownCodeBlock(args.FileName, args.Content)
			data = []byte(cleaned)
			mimeType = "text/markdown"

		default:
			// Reject unsupported formats — only .pdf, .html, .md are supported
			return nil, fmt.Errorf("unsupported file format %q: only .pdf, .html, and .md are supported", fileExt)
		}

		// Generate unique file ID to avoid overwriting existing files
		fileID := uuid.New().String()
		storedFileName := fileID + fileExt

		req := &artifact.SaveRequest{
			AppName:   ctx.AppName(),
			UserID:    ctx.UserID(),
			SessionID: ctx.SessionID(),
			FileName:  storedFileName,
			Part: &genai.Part{
				Text: string(data),
				InlineData: &genai.Blob{
					MIMEType: mimeType,
					Data:     data,
				},
			},
		}

		_, err := svc.Save(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to save file: %w", err)
		}

		// Generate file URL
		fileURL := toolutils.GenerateFileURL(cfg, ctx.AppName(), ctx.UserID(), ctx.SessionID(), storedFileName)

		// Get file size and format
		fileSize := len(data)
		fileFormat := toolutils.GetFileFormat(args.FileName)

		return map[string]any{
			"status":   "success",
			"filename": args.FileName,
			"file_id":  fileID,
			"url":      fileURL,
			"size":     fileSize,
			"format":   fileFormat,
		}, nil
	})
}

// looksLikeHTMLDocument reports whether content appears to be a raw HTML
// document rather than Markdown. The HTML output path expects Markdown and
// renders it internally, so a model that returns full HTML must be rejected.
// It only matches strong document-level signals to avoid flagging Markdown
// that legitimately embeds a stray inline tag.
//
// Models sometimes emit angle brackets as the literal 6-character escape
// sequences backslash-u-003c / backslash-u-003e (not real JSON unicode
// escapes), which survive JSON decoding as literal text. These are normalized
// back to < / > before scanning so such payloads are still detected.
func looksLikeHTMLDocument(content string) bool {
	lower := strings.ToLower(content)
	bs := "\\" // single backslash
	lower = strings.NewReplacer(
		bs+"u003c", "<",
		bs+"u003e", ">",
	).Replace(lower)
	for _, marker := range []string{"<!doctype html", "<html", "<head", "<body", "<style", "<script"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
