package providers

import (
	"fmt"

	at "github.com/UnderTreeTech/adk-go/artifact"
	"github.com/UnderTreeTech/adk-go/artifact/diskstorage"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
)

func init() {
	registry.RegisterServiceProvider("disk_artifact", diskArtifactProvider)
}

// diskArtifactProvider creates a disk-based artifact service.
//
// Config keys:
//   - rootDir (string, required): filesystem base directory for artifacts
//   - baseUrl (string, optional): URL for accessing stored artifacts
func diskArtifactProvider(config map[string]any) (any, error) {
	rootDir, _ := config["rootDir"].(string)
	if rootDir == "" {
		rootDir = "/tmp/artifacts"
	}

	baseUrl, _ := config["baseUrl"].(string)

	svc, err := diskstorage.NewDiskService(&at.Config{
		StorageType: "disk",
		FsBaseDir:   rootDir,
		FsBaseUrl:   baseUrl,
	})
	if err != nil {
		return nil, fmt.Errorf("disk_artifact: %w", err)
	}
	return svc, nil
}
