package registry

import (
	"fmt"
	"sync"

	adkagent "google.golang.org/adk/agent"

	"github.com/UnderTreeTech/adk-go/orchestration"
)

// CallbackRegistry resolves callback references to actual callback functions.
type CallbackRegistry interface {
	// RegisterBeforeAgent adds a BeforeAgentCallback under the given ref name.
	RegisterBeforeAgent(ref string, cb adkagent.BeforeAgentCallback) error

	// RegisterAfterAgent adds an AfterAgentCallback under the given ref name.
	RegisterAfterAgent(ref string, cb adkagent.AfterAgentCallback) error

	// GetBeforeAgent returns the BeforeAgentCallback for the given ref.
	GetBeforeAgent(ref string) (adkagent.BeforeAgentCallback, error)

	// GetAfterAgent returns the AfterAgentCallback for the given ref.
	GetAfterAgent(ref string) (adkagent.AfterAgentCallback, error)
}

// DefaultCallbackRegistry implements CallbackRegistry with concurrent-safe maps.
type DefaultCallbackRegistry struct {
	mu           sync.RWMutex
	beforeAgents map[string]adkagent.BeforeAgentCallback
	afterAgents  map[string]adkagent.AfterAgentCallback
}

// NewCallbackRegistry creates an empty DefaultCallbackRegistry.
func NewCallbackRegistry() *DefaultCallbackRegistry {
	return &DefaultCallbackRegistry{
		beforeAgents: make(map[string]adkagent.BeforeAgentCallback),
		afterAgents:  make(map[string]adkagent.AfterAgentCallback),
	}
}

func (r *DefaultCallbackRegistry) RegisterBeforeAgent(ref string, cb adkagent.BeforeAgentCallback) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.beforeAgents[ref]; exists {
		return fmt.Errorf("orchestration/registry: duplicate beforeAgent callback ref %q", ref)
	}
	r.beforeAgents[ref] = cb
	return nil
}

func (r *DefaultCallbackRegistry) RegisterAfterAgent(ref string, cb adkagent.AfterAgentCallback) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.afterAgents[ref]; exists {
		return fmt.Errorf("orchestration/registry: duplicate afterAgent callback ref %q", ref)
	}
	r.afterAgents[ref] = cb
	return nil
}

func (r *DefaultCallbackRegistry) GetBeforeAgent(ref string) (adkagent.BeforeAgentCallback, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cb, ok := r.beforeAgents[ref]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: beforeAgent callback ref %q not found", ref)
	}
	return cb, nil
}

func (r *DefaultCallbackRegistry) GetAfterAgent(ref string) (adkagent.AfterAgentCallback, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cb, ok := r.afterAgents[ref]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: afterAgent callback ref %q not found", ref)
	}
	return cb, nil
}

// RegisterFromRefs constructs callbacks from CallbackRef declarations using
// registered callback providers. The ServiceRegistry is available for
// providers that need infrastructure services.
func (r *DefaultCallbackRegistry) RegisterFromRefs(refs []orchestration.CallbackRef, svcReg ServiceRegistry) error {
	for _, ref := range refs {
		provider, err := GetCallbackProvider(ref.Provider)
		if err != nil {
			return fmt.Errorf("orchestration/registry: callback ref %q: %w", ref.Ref, err)
		}
		before, after, err := provider(ref.Config, svcReg)
		if err != nil {
			return fmt.Errorf("orchestration/registry: callback ref %q: provider %q: %w", ref.Ref, ref.Provider, err)
		}
		if before != nil {
			if err := r.RegisterBeforeAgent(ref.Ref, before); err != nil {
				return err
			}
		}
		if after != nil {
			if err := r.RegisterAfterAgent(ref.Ref, after); err != nil {
				return err
			}
		}
	}
	return nil
}
