package filegentool

import (
	"fmt"

	toolutils "github.com/UnderTreeTech/adk-go/tools"

	at "github.com/UnderTreeTech/adk-go/artifact"

	"github.com/UnderTreeTech/waterdrop/pkg/log"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/session"
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
		// Construct the SaveRequest.
		// We rely on ctx providing the necessary session information.
		// Note: The ADK tool.Context interface allows accessing session information.
		req := &artifact.SaveRequest{
			AppName:   ctx.AppName(),
			UserID:    ctx.UserID(),
			SessionID: ctx.SessionID(),
			FileName:  args.FileName,
			Part: &genai.Part{
				Text: args.Content,
				InlineData: &genai.Blob{
					MIMEType: mimeType,
					Data:     []byte(args.Content),
				},
			},
		}

		resp, err := svc.Save(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to save file: %w", err)
		}

		// 生成文件URL
		fileURL := toolutils.GenerateFileURL(cfg, ctx.AppName(), ctx.UserID(), ctx.SessionID(), args.FileName, resp.Version)

		// 获取文件大小和格式
		fileSize := len([]byte(args.Content))
		fileFormat := toolutils.GetFileFormat(args.FileName)

		// 构建文件信息
		fileInfo := map[string]any{
			"filename": args.FileName,
			"format":   fileFormat,
			//"content":      args.Content,
			"size":         fileSize,
			"download_url": fileURL,
		}

		// 将文件信息写入 session.State 的 artifacts 字段
		// 使用 temp: 前缀，因为这是单次 invocation 的临时状态
		artifactsKey := session.KeyPrefixTemp + "artifacts"
		var artifacts []map[string]any

		// 尝试读取现有的 artifacts
		if val, err := ctx.State().Get(artifactsKey); err == nil && val != nil {
			if existingArtifacts, ok := val.([]map[string]any); ok {
				artifacts = existingArtifacts
			}
		}

		// 追加新文件信息
		artifacts = append(artifacts, fileInfo)

		// 更新 session.State
		if err := ctx.State().Set(artifactsKey, artifacts); err != nil {
			// 记录错误但不影响工具执行
			log.Error(ctx, "failed to add temp file into to state", log.String("error", err.Error()))
		}

		return map[string]any{
			"status":   "success",
			"filename": args.FileName,
			"version":  resp.Version,
			"url":      fileURL,
			"size":     fileSize,
			"format":   fileFormat,
			//"content":  args.Content,
		}, nil
	})
}
