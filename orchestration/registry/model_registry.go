package registry

import (
	"fmt"
	"sync"

	"github.com/UnderTreeTech/adk-go/orchestration"
	"google.golang.org/adk/model"
)

// ModelRegistry resolves model references to model.LLM instances.
type ModelRegistry interface {
	// Register adds a model under the given ref name.
	Register(ref string, llm model.LLM) error

	// Get returns the model.LLM for the given ref.
	Get(ref string) (model.LLM, error)

	// Refs returns all registered model ref names.
	Refs() []string
}

// DefaultModelRegistry implements ModelRegistry with a concurrent-safe map.
type DefaultModelRegistry struct {
	mu     sync.RWMutex
	models map[string]model.LLM
}

// NewModelRegistry creates an empty DefaultModelRegistry.
func NewModelRegistry() *DefaultModelRegistry {
	return &DefaultModelRegistry{
		models: make(map[string]model.LLM),
	}
}

func (r *DefaultModelRegistry) Register(ref string, llm model.LLM) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.models[ref]; exists {
		return fmt.Errorf("orchestration/registry: duplicate model ref %q", ref)
	}
	r.models[ref] = llm
	return nil
}

func (r *DefaultModelRegistry) Get(ref string) (model.LLM, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	llm, ok := r.models[ref]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: model ref %q not found", ref)
	}
	return llm, nil
}

func (r *DefaultModelRegistry) Refs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	refs := make([]string, 0, len(r.models))
	for ref := range r.models {
		refs = append(refs, ref)
	}
	return refs
}

// RegisterFromRefs constructs models from ModelRef declarations using
// registered model providers. The ServiceRegistry is available for
// providers that need infrastructure services.
func (r *DefaultModelRegistry) RegisterFromRefs(refs []orchestration.ModelRef, svcReg ServiceRegistry) error {
	for _, ref := range refs {
		provider, err := GetModelProvider(ref.Provider)
		if err != nil {
			return fmt.Errorf("orchestration/registry: model ref %q: %w", ref.Ref, err)
		}
		llm, err := provider(ref.Config, svcReg)
		if err != nil {
			return fmt.Errorf("orchestration/registry: model ref %q: provider %q: %w", ref.Ref, ref.Provider, err)
		}
		if err := r.Register(ref.Ref, llm); err != nil {
			return err
		}
	}
	return nil
}
