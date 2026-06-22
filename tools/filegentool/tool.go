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
	FileName string `json:"filename" description:"The name of the file to create. Only .pdf, .html, and .md formats are supported, e.g. 'report.pdf', 'report.html', 'report.md'."`
	Content  string `json:"content" description:"Markdown-formatted content. For PDF/HTML it will be rendered from Markdown; for .md it is saved as-is."`
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
		Description: "Generates a document file and saves it to the storage server. ONLY supports three formats: Markdown (.md), HTML (.html/.htm), and PDF (.pdf). For HTML and PDF, provide Markdown-formatted content and it will be automatically rendered to the target format. For Markdown, content is saved as-is. Do NOT use this tool for other file types (e.g. .docx, .xlsx, .pptx, .csv, .json, .xml, code files, etc.) — those formats are not supported and will fail. Use this tool only when the user explicitly asks to generate a document in .md, .html, or .pdf format.",
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
