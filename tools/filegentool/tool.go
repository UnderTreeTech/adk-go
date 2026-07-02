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
//
// Behavior by format:
//   - .md: Content is saved as-is (with code-block markers cleaned via CleanMarkdownCodeBlock).
//   - .html/.htm: Content is saved as-is (with code-block markers cleaned via CleanMarkdownCodeBlock).
//   - .pdf: Content is treated as Markdown and rendered to PDF.
type Args struct {
	FileName string `json:"filename" description:"要创建的文件名。仅支持 .pdf、.html 和 .md 三种格式，例如 'report.pdf'、'report.html'、'report.md'。"`
	Content  string `json:"content" description:"文档正文内容。对于 .md 和 .html 格式，传入的内容会原样保存（仅清理代码块标记）。对于 .pdf 格式，内容必须是纯 Markdown 文本，工具会自动渲染为 PDF。"`
}

// New creates a new file generation tool instance that supports three output
// formats only: Markdown (saved as-is), HTML (saved as-is), and PDF
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
		Description: "生成文档文件并保存到存储服务器。仅支持三种格式：Markdown (.md)、HTML (.html/.htm) 和 PDF (.pdf)。各格式的处理方式如下：\n- .md：内容原样保存（仅清理代码块标记）。\n- .html/.htm：内容原样保存（仅清理代码块标记）。\n- .pdf：内容作为 Markdown 渲染为 PDF。\n不要将此工具用于其他文件类型（如 .docx、.xlsx、.pptx、.csv、.json、.xml、代码文件等），这些格式不受支持且会失败。",
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
			// Save HTML content as-is (with code-block marker cleanup)
			cleaned := toolutils.CleanMarkdownCodeBlock(args.FileName, args.Content)
			data = []byte(cleaned)
			mimeType = "text/html"

		case ".md":
			// Save Markdown content as-is (with code-block marker cleanup)
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
