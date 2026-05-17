package tool

import (
	"fmt"
	"path/filepath"
	"strings"

	at "github.com/UnderTreeTech/adk-go/artifact"
)

// CleanMarkdownCodeBlock 清理markdown代码块标记
// 移除开头的 ```markdown 或 ``` 以及结尾的 ```
// 参数:
//   - filename: 文件名，用于判断是否为markdown文件
//   - content: 原始内容
//
// 返回:
//   - 清理后的内容
func CleanMarkdownCodeBlock(filename, content string) string {
	// 只处理markdown文件
	if !strings.HasSuffix(filename, ".md") {
		return content
	}

	trimmed := strings.TrimSpace(content)

	// 检查是否以 ``` 开头
	if !strings.HasPrefix(trimmed, "```") {
		return content
	}

	// 找到第一个换行符，跳过第一行的 ```markdown 或 ```
	firstNewline := strings.Index(trimmed, "\n")
	if firstNewline == -1 {
		// 只有一行，直接返回原内容
		return content
	}

	// 跳过第一行
	contentWithoutFirstLine := trimmed[firstNewline+1:]

	// 检查是否以 ``` 结尾
	if strings.HasSuffix(contentWithoutFirstLine, "```") {
		// 移除最后的 ```
		contentWithoutFirstLine = strings.TrimSuffix(contentWithoutFirstLine, "```")
		// 移除最后的换行符
		contentWithoutFirstLine = strings.TrimRight(contentWithoutFirstLine, "\n")
	}

	return contentWithoutFirstLine
}

// GenerateFileURL 生成文件下载URL
// 参数:
//   - cfg: artifact配置
//   - appName: 应用名称
//   - userID: 用户ID
//   - sessionID: 会话ID
//   - fileName: 文件名
//   - version: 版本号
//
// 返回:
//   - 文件下载URL
func GenerateFileURL(cfg *at.Config, appName, userID, sessionID, fileName string, version int64) string {
	if cfg.StorageType == "s3" {
		schema := cfg.ExternalSchema
		if schema == "" {
			schema = "http"
		}
		endpoint := cfg.ExternalEndpoint
		if endpoint == "" {
			endpoint = cfg.InternalEndpoint
		}
		// S3 URL 格式: {schema}://{endpoint}/{bucket}/{appName}/{userID}/{sessionID}/{fileName}/{version}
		return fmt.Sprintf("%s://%s/%s/%s/%s/%s/%s/%d",
			schema, endpoint, cfg.Bucket, appName, userID, sessionID, fileName, version)
	} else {
		// 磁盘存储 URL 格式: {FsBaseUrl}/{appName}/{userID}/{sessionID}/{fileName}/{version}
		return fmt.Sprintf("%s/%s/%s/%s/%s/%d",
			cfg.FsBaseUrl, appName, userID, sessionID, fileName, version)
	}
}


// GetMimeType 根据文件扩展名获取 MIME 类型
func GetMimeType(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	mimeTypes := map[string]string{
		// 文本类
		".md":   "text/markdown",
		".txt":  "text/plain",
		".csv":  "text/csv",
		".html": "text/html",
		".htm":  "text/html",
		".css":  "text/css",
		".js":   "text/javascript",
		".json": "application/json",
		".xml":  "application/xml",
		".yaml": "application/yaml",
		".yml":  "application/yaml",

		// 图片类
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".bmp":  "image/bmp",
		".svg":  "image/svg+xml",
		".webp": "image/webp",
		".ico":  "image/x-icon",
		".tiff": "image/tiff",
		".tif":  "image/tiff",

		// 文档类
		".pdf":  "application/pdf",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".ppt":  "application/vnd.ms-powerpoint",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".odt":  "application/vnd.oasis.opendocument.text",
		".ods":  "application/vnd.oasis.opendocument.spreadsheet",
		".odp":  "application/vnd.oasis.opendocument.presentation",

		// 压缩包类
		".zip": "application/zip",
		".rar": "application/x-rar-compressed",
		".7z":  "application/x-7z-compressed",
		".tar": "application/x-tar",
		".gz":  "application/gzip",

		// 音视频类
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".ogg":  "audio/ogg",
		".mp4":  "video/mp4",
		".mov":  "video/quicktime",
		".avi":  "video/x-msvideo",
		".mkv":  "video/x-matroska",
		".webm": "video/webm",

		// 编程相关
		".py":   "text/x-python",
		".java": "text/x-java-source",
		".c":    "text/x-c",
		".cpp":  "text/x-c++",
		".h":    "text/x-c",
		".sh":   "text/x-shellscript",
		".php":  "text/x-php",

		// 其他常见类型
		".exe": "application/octet-stream",
		".dll": "application/octet-stream",
		".iso": "application/x-iso9660-image",
		".msi": "application/x-msdownload",
		".apk": "application/vnd.android.package-archive",
		".deb": "application/x-debian-package",
		".rpm": "application/x-rpm",
	}

	if mimeType, ok := mimeTypes[ext]; ok {
		return mimeType
	}

	// 默认返回 text/plain
	return "text/plain"
}

// GetFileFormat 根据文件名获取文件格式（扩展名，不含点号）
func GetFileFormat(fileName string) string {
	ext := filepath.Ext(fileName)
	if ext == "" {
		return "unknown"
	}
	// 移除点号并返回小写扩展名
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}