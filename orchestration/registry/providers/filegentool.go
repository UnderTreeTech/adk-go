package providers

import (
	"fmt"

	at "github.com/UnderTreeTech/adk-go/artifact"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
	"github.com/UnderTreeTech/adk-go/tools/filegentool"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/tool"
)

func init() {
	registry.RegisterToolProvider("filegentool", fileGenToolProvider)
}

// fileGenToolProvider creates a file generation tool.
//
// Config keys:
//   - serviceRef (string, required): reference to an artifact service in
//     the ServiceRegistry (e.g., "disk_artifact")
//   - baseUrl (string, optional): base URL for artifact access
func fileGenToolProvider(config map[string]any, svcReg registry.ServiceRegistry) (tool.Tool, error) {
	serviceRef, _ := config["serviceRef"].(string)
	if serviceRef == "" {
		return nil, fmt.Errorf("filegentool: config[\"serviceRef\"] is required (must reference an artifact service)")
	}

	svcAny, err := svcReg.Get(serviceRef)
	if err != nil {
		return nil, fmt.Errorf("filegentool: resolve service ref %q: %w", serviceRef, err)
	}

	svc, ok := svcAny.(artifact.Service)
	if !ok {
		return nil, fmt.Errorf("filegentool: service ref %q is not an artifact.Service (got %T)", serviceRef, svcAny)
	}

	// Build artifact config for URL generation
	baseUrl, _ := config["baseUrl"].(string)

	t, err := filegentool.New(svc, &at.Config{
		FsBaseUrl: baseUrl,
	})
	if err != nil {
		return nil, fmt.Errorf("filegentool: %w", err)
	}
	return t, nil
}
