package filegentool

import (
	"fmt"
	"path/filepath"

	"github.com/google/uuid"

	at "github.com/UnderTreeTech/adk-go/artifact"

	toolutils "github.com/UnderTreeTech/adk-go/tools"

	"google.golang.org/adk/artifact"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

// Args defines the arguments for the file generation tool.
type Args struct {
	FileName string `json:"filename" description:"The name of the file to create, e.g., 'report.md', 'index.html', 'script.py'."`
	Content  string `json:"content" description:"The complete text content of the file."`
}

// New creates a new file generation tool instance.
func New(svc artifact.Service, cfg *at.Config) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "generate_file",
		Description: "Generates a file with the specified content and saves it to the storage server. Use this tool when the user explicitly asks to generate a file (markdown, html, code, etc.) or when the output is best represented as a file.",
	}, func(ctx tool.Context, args Args) (map[string]any, error) {
		if args.FileName == "" {
			return nil, fmt.Errorf("filename is required")
		}
		if args.Content == "" {
			return nil, fmt.Errorf("content is required")
		}

		mimeType := toolutils.GetMimeType(args.FileName)
		fileExt := filepath.Ext(args.FileName)

		// 生成唯一文件ID，避免同名文件互相覆盖
		fileID := uuid.New().String()
		storedFileName := fileID + fileExt

		req := &artifact.SaveRequest{
			AppName:   ctx.AppName(),
			UserID:    ctx.UserID(),
			SessionID: ctx.SessionID(),
			FileName:  storedFileName,
			Part: &genai.Part{
				Text: args.Content,
				InlineData: &genai.Blob{
					MIMEType: mimeType,
					Data:     []byte(args.Content),
				},
			},
		}

		_, err := svc.Save(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to save file: %w", err)
		}

		// 生成文件URL
		fileURL := toolutils.GenerateFileURL(cfg, ctx.AppName(), ctx.UserID(), ctx.SessionID(), storedFileName)

		// 获取文件大小和格式
		fileSize := len([]byte(args.Content))
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
