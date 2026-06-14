package registry

import (
	"fmt"
	"sync"

	"github.com/UnderTreeTech/adk-go/orchestration"
)

// ServiceRegistry resolves service references to infrastructure service instances.
// Services are runtime objects like artifact.Service, session stores, etc.
type ServiceRegistry interface {
	// Register adds a service under the given ref name.
	Register(ref string, svc any) error

	// Get returns the service for the given ref.
	Get(ref string) (any, error)

	// Refs returns all registered service ref names.
	Refs() []string
}

// DefaultServiceRegistry implements ServiceRegistry with a concurrent-safe map.
type DefaultServiceRegistry struct {
	mu       sync.RWMutex
	services map[string]any
}

// NewServiceRegistry creates an empty DefaultServiceRegistry.
func NewServiceRegistry() *DefaultServiceRegistry {
	return &DefaultServiceRegistry{
		services: make(map[string]any),
	}
}

func (r *DefaultServiceRegistry) Register(ref string, svc any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.services[ref]; exists {
		return fmt.Errorf("orchestration/registry: duplicate service ref %q", ref)
	}
	r.services[ref] = svc
	return nil
}

func (r *DefaultServiceRegistry) Get(ref string) (any, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[ref]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: service ref %q not found", ref)
	}
	return svc, nil
}

func (r *DefaultServiceRegistry) Refs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	refs := make([]string, 0, len(r.services))
	for ref := range r.services {
		refs = append(refs, ref)
	}
	return refs
}

// RegisterFromRefs constructs services from ServiceRef declarations using
// registered service providers. This is called during bootstrap before
// building models and tools.
func (r *DefaultServiceRegistry) RegisterFromRefs(refs []orchestration.ServiceRef) error {
	for _, ref := range refs {
		provider, err := GetServiceProvider(ref.Provider)
		if err != nil {
			return fmt.Errorf("orchestration/registry: service ref %q: %w", ref.Ref, err)
		}
		svc, err := provider(ref.Config)
		if err != nil {
			return fmt.Errorf("orchestration/registry: service ref %q: provider %q: %w", ref.Ref, ref.Provider, err)
		}
		if err := r.Register(ref.Ref, svc); err != nil {
			return err
		}
	}
	return nil
}
