package appendfiletool

import (
	"fmt"
	"os"
	"strings"

	at "github.com/UnderTreeTech/adk-go/artifact"

	toolutils "github.com/UnderTreeTech/adk-go/tools"

	"github.com/UnderTreeTech/waterdrop/pkg/log"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

// Args defines the arguments for the append to file tool.
type Args struct {
	FileName string `json:"filename" description:"The name of the file to append content to, e.g., 'report.md', 'index.html', 'script.py'. If the file doesn't exist, it will be created."`
	Content  string `json:"content" description:"The text content to append to the file. This will be added to the end of the existing file content."`
	IsLast   bool   `json:"is_last" description:"Set to true if this is the last chunk of content for this file. Default is false."`
}

// New creates a new append to file tool instance.
func New(svc artifact.Service, cfg *at.Config) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "generate_file",
		Description: `Create/append content to a file.
					small file(<4000 tokens): set 'is_last'=true.
					large file(>4000 tokens): multiple calls, 'is_last'=true only for the last chunk.`,
	}, func(ctx tool.Context, args Args) (map[string]any, error) {
		if args.FileName == "" {
			return nil, fmt.Errorf("filename is required")
		}
		if args.Content == "" {
			return nil, fmt.Errorf("content is required")
		}

		log.Debug(ctx, "generate_file tool called",
			log.String("filename", args.FileName),
			log.String("is_last", fmt.Sprintf("%v", args.IsLast)),
			log.Int("content_length", len(args.Content)))

		mimeType := toolutils.GetMimeType(args.FileName)

		// 从 session.State 中获取文件的临时路径和累计大小
		tempKey := session.KeyPrefixTemp + "file_" + args.FileName
		var tempFilePath string
		var currentSize int

		// 尝试获取现有的临时文件路径
		if val, err := ctx.State().Get(tempKey); err == nil && val != nil {
			if info, ok := val.(map[string]any); ok {
				if path, ok := info["temp_path"].(string); ok {
					tempFilePath = path
				}
				if size, ok := info["size"].(int); ok {
					currentSize = size
				}
			}
		}

		// 小文件一次性生成（is_last为true且没有临时文件）
		if args.IsLast && tempFilePath == "" {
			log.Debug(ctx, "generating small file directly without temp file",
				log.String("filename", args.FileName),
				log.Int("file_size", len(args.Content)))

			// 直接保存到 artifact service，不创建临时文件
			contentBytes := []byte(args.Content)
			req := &artifact.SaveRequest{
				AppName:   ctx.AppName(),
				UserID:    ctx.UserID(),
				SessionID: ctx.SessionID(),
				FileName:  args.FileName,
				Part: &genai.Part{
					Text: args.Content,
					InlineData: &genai.Blob{
						MIMEType: mimeType,
						Data:     contentBytes,
					},
				},
			}

			resp, err := svc.Save(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("failed to save file: %w", err)
			}

			// 生成文件URL
			fileURL := toolutils.GenerateFileURL(cfg, ctx.AppName(), ctx.UserID(), ctx.SessionID(), args.FileName, resp.Version)
			fileFormat := toolutils.GetFileFormat(args.FileName)
			fileSize := len(contentBytes)

			// 将文件信息写入 session.State 的 artifacts 字段
			artifactsKey := session.KeyPrefixTemp + "artifacts"
			var artifacts []map[string]any

			if val, err := ctx.State().Get(artifactsKey); err == nil && val != nil {
				if existingArtifacts, ok := val.([]map[string]any); ok {
					artifacts = existingArtifacts
				}
			}

			fileInfo := map[string]any{
				"filename":     args.FileName,
				"format":       fileFormat,
				"size":         fileSize,
				"download_url": fileURL,
			}
			artifacts = append(artifacts, fileInfo)

			if err := ctx.State().Set(artifactsKey, artifacts); err != nil {
				log.Error(ctx, "failed to add file info to state", log.String("error", err.Error()))
			}

			log.Debug(ctx, "small file generation completed",
				log.String("filename", args.FileName),
				log.String("url", fileURL),
				log.Int("size", fileSize))

			return map[string]any{
				"status":   "completed",
				"filename": args.FileName,
				"version":  resp.Version,
				"url":      fileURL,
				"size":     fileSize,
				"format":   fileFormat,
				"message":  fmt.Sprintf("File '%s' has been successfully created with %d bytes", args.FileName, fileSize),
			}, nil
		}

		// 大文件分块生成逻辑（需要临时文件）
		// 如果没有临时文件，创建一个
		if tempFilePath == "" {
			// 构建唯一的临时文件名，包含 appName(哈希脱敏), userID, sessionID 和 filename
			// 使用下划线替换文件名中的特殊字符，避免路径问题
			safeFileName := strings.ReplaceAll(args.FileName, "/", "_")
			safeFileName = strings.ReplaceAll(safeFileName, "\\", "_")

			// 创建临时文件名模式，确保多用户、多会话不会冲突
			pattern := fmt.Sprintf("append_%s_%s_%s_%s_*.tmp",
				at.HashAppName(ctx.AppName()), ctx.UserID(), ctx.SessionID(), safeFileName)

			// 使用系统临时目录创建临时文件
			file, err := os.CreateTemp("", pattern)
			if err != nil {
				return nil, fmt.Errorf("failed to create temp file: %w", err)
			}
			tempFilePath = file.Name()
			file.Close()

			log.Debug(ctx, "creating new temp file for large file generation",
				log.String("filename", args.FileName),
				log.String("temp_path", tempFilePath),
				log.String("appName", at.HashAppName(ctx.AppName())),
				log.String("userID", ctx.UserID()),
				log.String("sessionID", ctx.SessionID()))
		} else {
			log.Debug(ctx, "using existing temp file for large file generation",
				log.String("filename", args.FileName),
				log.String("temp_path", tempFilePath),
				log.Int("current_size", currentSize))
		}

		// 追加内容到临时文件
		file, err := os.OpenFile(tempFilePath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open temp file: %w", err)
		}
		defer file.Close()

		n, err := file.WriteString(args.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to append content: %w", err)
		}

		currentSize += n

		log.Debug(ctx, "content appended to temp file",
			log.String("filename", args.FileName),
			log.Int("chunk_size", n),
			log.Int("total_size", currentSize),
			log.String("is_last", fmt.Sprintf("%v", args.IsLast)))

		// 更新 session.State 中的临时文件信息
		if err := ctx.State().Set(tempKey, map[string]any{
			"temp_path": tempFilePath,
			"size":      currentSize,
		}); err != nil {
			log.Error(ctx, "failed to update temp file info in state", log.String("error", err.Error()))
		}

		// 如果这是最后一块内容，将文件保存到 artifact service
		if args.IsLast {
			log.Debug(ctx, "finalizing large file from temp file",
				log.String("filename", args.FileName),
				log.Int("total_size", currentSize))

			// 读取完整的文件内容
			fileData, err := os.ReadFile(tempFilePath)
			if err != nil {
				return nil, fmt.Errorf("failed to read temp file: %w", err)
			}

			// 保存到 artifact service
			req := &artifact.SaveRequest{
				AppName:   ctx.AppName(),
				UserID:    ctx.UserID(),
				SessionID: ctx.SessionID(),
				FileName:  args.FileName,
				Part: &genai.Part{
					Text: string(fileData),
					InlineData: &genai.Blob{
						MIMEType: mimeType,
						Data:     fileData,
					},
				},
			}

			resp, err := svc.Save(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("failed to save file: %w", err)
			}

			// 生成文件URL
			fileURL := toolutils.GenerateFileURL(cfg, ctx.AppName(), ctx.UserID(), ctx.SessionID(), args.FileName, resp.Version)
			fileFormat := toolutils.GetFileFormat(args.FileName)

			// 将文件信息写入 session.State 的 artifacts 字段
			artifactsKey := session.KeyPrefixTemp + "artifacts"
			var artifacts []map[string]any

			if val, err := ctx.State().Get(artifactsKey); err == nil && val != nil {
				if existingArtifacts, ok := val.([]map[string]any); ok {
					artifacts = existingArtifacts
				}
			}

			fileInfo := map[string]any{
				"filename":     args.FileName,
				"format":       fileFormat,
				"size":         currentSize,
				"download_url": fileURL,
			}
			artifacts = append(artifacts, fileInfo)

			if err := ctx.State().Set(artifactsKey, artifacts); err != nil {
				log.Error(ctx, "failed to add file info to state", log.String("error", err.Error()))
			}

			// 清理临时文件和状态
			os.Remove(tempFilePath)
			// 清除临时状态（将值设为 nil）
			ctx.State().Set(tempKey, nil)

			log.Debug(ctx, "large file generation completed",
				log.String("filename", args.FileName),
				log.String("url", fileURL),
				log.Int("total_size", currentSize))

			return map[string]any{
				"status":   "completed",
				"filename": args.FileName,
				"version":  resp.Version,
				"url":      fileURL,
				"size":     currentSize,
				"format":   fileFormat,
				"message":  "File has been successfully created. You can download it using the provided URL.",
			}, nil
		}

		// 如果不是最后一块，返回追加成功的信息
		log.Debug(ctx, "chunk appended, waiting for more content",
			log.String("filename", args.FileName),
			log.Int("chunk_size", n),
			log.Int("total_size", currentSize))

		return map[string]any{
			"status":     "appending",
			"filename":   args.FileName,
			"chunk_size": n,
			"total_size": currentSize,
			"message":    fmt.Sprintf("Successfully appended %d bytes. Total size: %d bytes. Continue appending or set 'is_last' to true to finalize.", n, currentSize),
		}, nil
	})
}
