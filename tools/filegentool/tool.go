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
// When the filename ends with .pdf or .html, the Content field is treated as
// Markdown and rendered to the target format before saving. For other formats
// (e.g. .md, .txt, .py) the content is saved as-is.
type Args struct {
	FileName string `json:"filename" description:"The name of the file to create, e.g. 'report.pdf', 'report.html', 'report.md'."`
	Content  string `json:"content" description:"Markdown-formatted content for PDF/HTML output, or raw text for other file types."`
}

// New creates a new file generation tool instance that supports multiple output
// formats: Markdown (saved as-is), HTML (rendered from Markdown), and PDF
// (rendered from Markdown with CJK support).
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
		Description: "Generates a file with the specified content and saves it to the storage server. Supports Markdown (.md), HTML (.html), and PDF (.pdf) generation. For HTML and PDF, provide Markdown-formatted content and it will be automatically rendered to the target format. For other file types (code, text, etc.), the content is saved as-is. Use this tool when the user explicitly asks to generate a file or when the output is best represented as a formatted document.",
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
			// Save as-is for other formats
			data = []byte(args.Content)
			mimeType = toolutils.GetMimeType(args.FileName)
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
