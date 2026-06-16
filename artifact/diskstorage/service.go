package diskstorage

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	at "github.com/UnderTreeTech/adk-go/artifact"

	"google.golang.org/adk/artifact"
	"google.golang.org/genai"
)

// diskService is a local filesystem implementation of the Service.
type diskService struct {
	baseDir string
	cfg     *at.Config
}

// NewDiskService creates a disk storage service.
func NewDiskService(cfg *at.Config) (artifact.Service, error) {
	if cfg.FsBaseDir == "" {
		return nil, fmt.Errorf("FsBaseDir is required for disk storage")
	}
	// Ensure base directory exists
	if err := os.MkdirAll(cfg.FsBaseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}
	return &diskService{
		baseDir: cfg.FsBaseDir,
		cfg:     cfg,
	}, nil
}

// fileHasUserNamespace checks if a filename indicates a user-namespaced blob.
func fileHasUserNamespace(filename string) bool {
	return strings.HasPrefix(filename, "user:")
}

// buildPath constructs the file path on disk.
func (s *diskService) buildPath(appName, userID, sessionID, fileName string) string {
	if fileHasUserNamespace(fileName) {
		return filepath.Join(s.baseDir, appName, userID, "user", fileName)
	}
	return filepath.Join(s.baseDir, appName, userID, sessionID, fileName)
}

// buildSessionDir constructs the directory path for a session.
func (s *diskService) buildSessionDir(appName, userID, sessionID string) string {
	return filepath.Join(s.baseDir, appName, userID, sessionID)
}

// buildUserDir constructs the directory path for a user's global namespace.
func (s *diskService) buildUserDir(appName, userID string) string {
	return filepath.Join(s.baseDir, appName, userID, "user")
}

// Save implements [artifact.Service]
func (s *diskService) Save(ctx context.Context, req *artifact.SaveRequest) (*artifact.SaveResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName
	newArtifact := req.Part

	filePath := s.buildPath(appName, userID, sessionID, fileName)
	dirPath := filepath.Dir(filePath)

	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	var data []byte
	if newArtifact.InlineData != nil {
		data = newArtifact.InlineData.Data
	} else {
		data = []byte(newArtifact.Text)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write file to disk: %w", err)
	}

	return &artifact.SaveResponse{Version: 0}, nil
}

// Delete implements [artifact.Service]
func (s *diskService) Delete(ctx context.Context, req *artifact.DeleteRequest) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName

	filePath := s.buildPath(appName, userID, sessionID, fileName)
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to delete artifact: %w", err)
	}

	return nil
}

// Load implements [artifact.Service]
func (s *diskService) Load(ctx context.Context, req *artifact.LoadRequest) (*artifact.LoadResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName

	filePath := s.buildPath(appName, userID, sessionID, fileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		return nil, fmt.Errorf("failed to read artifact: %w", err)
	}

	// Try to determine content type from extension
	contentType := mime.TypeByExtension(filepath.Ext(fileName))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	part := genai.NewPartFromBytes(data, contentType)
	return &artifact.LoadResponse{Part: part}, nil
}

// fetchFilenamesFromDir is a reusable helper function.
func (s *diskService) fetchFilenamesFromDir(dirPath string, filenamesSet map[string]bool) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			filenamesSet[entry.Name()] = true
		}
	}
	return nil
}

// List implements [artifact.Service]
func (s *diskService) List(ctx context.Context, req *artifact.ListRequest) (*artifact.ListResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID := at.HashAppName(req.AppName), req.UserID, req.SessionID
	filenamesSet := map[string]bool{}

	// Fetch filenames for the session
	sessionDir := s.buildSessionDir(appName, userID, sessionID)
	if err := s.fetchFilenamesFromDir(sessionDir, filenamesSet); err != nil {
		return nil, fmt.Errorf("failed to list session filenames: %w", err)
	}

	// Fetch filenames for the user
	userDir := s.buildUserDir(appName, userID)
	if err := s.fetchFilenamesFromDir(userDir, filenamesSet); err != nil {
		return nil, fmt.Errorf("failed to list user filenames: %w", err)
	}

	filenames := slices.Collect(maps.Keys(filenamesSet))
	sort.Strings(filenames)
	return &artifact.ListResponse{FileNames: filenames}, nil
}

// Versions implements [artifact.Service]
func (s *diskService) Versions(ctx context.Context, req *artifact.VersionsRequest) (*artifact.VersionsResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName

	filePath := s.buildPath(appName, userID, sessionID, fileName)
	_, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		return nil, fmt.Errorf("failed to check artifact existence: %w", err)
	}

	return &artifact.VersionsResponse{Versions: []int64{0}}, nil
}

// GetArtifactVersion implements [artifact.Service] and returns the metadata for a specific version.
func (s *diskService) GetArtifactVersion(ctx context.Context, req *artifact.GetArtifactVersionRequest) (*artifact.GetArtifactVersionResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName

	filePath := s.buildPath(appName, userID, sessionID, fileName)

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		return nil, fmt.Errorf("failed to stat artifact: %w", err)
	}

	// Determine MIME type from file extension
	mimeType := mime.TypeByExtension(filepath.Ext(fileName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Build canonical URI
	var canonicalURI string
	if s.cfg.FsBaseUrl != "" {
		canonicalURI = strings.TrimRight(s.cfg.FsBaseUrl, "/") + "/" + strings.TrimLeft(filePath, "/")
	} else {
		canonicalURI = "file://" + filePath
	}

	return &artifact.GetArtifactVersionResponse{
		ArtifactVersion: &artifact.ArtifactVersion{
			Version:      0,
			CanonicalURI: canonicalURI,
			CreateTime:   float64(info.ModTime().Unix()),
			MimeType:     mimeType,
		},
	}, nil
}

var _ artifact.Service = (*diskService)(nil)