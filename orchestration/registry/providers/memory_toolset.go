package providers

import (
	"fmt"

	"github.com/UnderTreeTech/adk-go/memory/memorytypes"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
	"github.com/UnderTreeTech/adk-go/tools/memory"
	"google.golang.org/adk/tool"
)

func init() {
	registry.RegisterToolProvider("memory_toolset", memoryToolsetProvider)
}

// memoryToolsetProvider creates a memory toolset with search_memory,
// save_to_memory, and optionally update_memory and delete_memory tools.
//
// Config keys:
//   - serviceRef (string, required): reference to a memory service in the
//     ServiceRegistry that implements memorytypes.MemoryService
//   - appName (string, required): application name for scoping memory operations
//   - disableExtendedTools (bool, optional): disable update/delete tools
func memoryToolsetProvider(config map[string]any, svcReg registry.ServiceRegistry) (tool.Tool, error) {
	serviceRef, _ := config["serviceRef"].(string)
	if serviceRef == "" {
		return nil, fmt.Errorf("memory_toolset: config[\"serviceRef\"] is required (must reference a memory service)")
	}

	svcAny, err := svcReg.Get(serviceRef)
	if err != nil {
		return nil, fmt.Errorf("memory_toolset: resolve service ref %q: %w", serviceRef, err)
	}

	memSvc, ok := svcAny.(memorytypes.MemoryService)
	if !ok {
		return nil, fmt.Errorf("memory_toolset: service ref %q is not a MemoryService (got %T)", serviceRef, svcAny)
	}

	appName, _ := config["appName"].(string)
	if appName == "" {
		return nil, fmt.Errorf("memory_toolset: config[\"appName\"] is required")
	}

	disableExtended, _ := config["disableExtendedTools"].(bool)

	ts, err := memory.NewToolset(memory.ToolsetConfig{
		MemoryService:        memSvc,
		AppName:              appName,
		DisableExtendedTools: disableExtended,
	})
	if err != nil {
		return nil, fmt.Errorf("memory_toolset: %w", err)
	}

	// The Toolset implements tool.Toolset, not tool.Tool directly.
	// We need to return a tool.Tool, but Toolset provides multiple tools.
	// For now, return an error indicating that memory_toolset should be used
	// as a toolset (not a single tool). The builder handles toolsets separately.
	// Actually, the AgentNode schema has a "toolsets" field for this purpose.
	// However, since our current schema only supports "tools" (not "toolsets"),
	// we'll return an error suggesting to use the toolset field.
	_ = ts
	return nil, fmt.Errorf("memory_toolset: use as a toolset (not a single tool); add toolset support to the schema and builder")
}
