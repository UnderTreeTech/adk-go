package registry

import (
	"fmt"
	"sync"

	"github.com/UnderTreeTech/adk-go/orchestration"
	"google.golang.org/adk/tool"
)

// ToolRegistry resolves tool references to tool.Tool instances.
type ToolRegistry interface {
	// Register adds a tool under the given ref name.
	Register(ref string, t tool.Tool) error

	// Get returns the tool.Tool for the given ref.
	Get(ref string) (tool.Tool, error)

	// Refs returns all registered tool ref names.
	Refs() []string
}

// DefaultToolRegistry implements ToolRegistry with a concurrent-safe map.
type DefaultToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]tool.Tool
}

// NewToolRegistry creates an empty DefaultToolRegistry.
func NewToolRegistry() *DefaultToolRegistry {
	return &DefaultToolRegistry{
		tools: make(map[string]tool.Tool),
	}
}

func (r *DefaultToolRegistry) Register(ref string, t tool.Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[ref]; exists {
		return fmt.Errorf("orchestration/registry: duplicate tool ref %q", ref)
	}
	r.tools[ref] = t
	return nil
}

func (r *DefaultToolRegistry) Get(ref string) (tool.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[ref]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: tool ref %q not found", ref)
	}
	return t, nil
}

func (r *DefaultToolRegistry) Refs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	refs := make([]string, 0, len(r.tools))
	for ref := range r.tools {
		refs = append(refs, ref)
	}
	return refs
}

// RegisterFromRefs constructs tools from ToolRef declarations using
// registered tool providers. The ServiceRegistry is available for
// providers that need infrastructure services (e.g., artifact service).
func (r *DefaultToolRegistry) RegisterFromRefs(refs []orchestration.ToolRef, svcReg ServiceRegistry) error {
	for _, ref := range refs {
		provider, err := GetToolProvider(ref.Provider)
		if err != nil {
			return fmt.Errorf("orchestration/registry: tool ref %q: %w", ref.Ref, err)
		}
		t, err := provider(ref.Config, svcReg)
		if err != nil {
			return fmt.Errorf("orchestration/registry: tool ref %q: provider %q: %w", ref.Ref, ref.Provider, err)
		}
		if err := r.Register(ref.Ref, t); err != nil {
			return err
		}
	}
	return nil
}
