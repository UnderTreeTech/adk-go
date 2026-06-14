package registry

import (
	"fmt"
	"sync"

	"github.com/UnderTreeTech/adk-go/orchestration"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// ---- Service Provider ----

// ServiceProvider constructs an infrastructure service from provider-specific config.
// The returned value is typically an interface like artifact.Service.
type ServiceProvider func(config map[string]any) (any, error)

var (
	serviceProvidersMu sync.RWMutex
	serviceProviders   = make(map[string]ServiceProvider)
)

// RegisterServiceProvider registers a named service provider (e.g., "disk_artifact").
func RegisterServiceProvider(name string, provider ServiceProvider) {
	serviceProvidersMu.Lock()
	defer serviceProvidersMu.Unlock()
	serviceProviders[name] = provider
}

// GetModelProvider returns the service provider by name.
func GetServiceProvider(name string) (ServiceProvider, error) {
	serviceProvidersMu.RLock()
	defer serviceProvidersMu.RUnlock()
	p, ok := serviceProviders[name]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: unknown service provider %q", name)
	}
	return p, nil
}

// ---- Model Provider ----

// ModelProvider constructs a model.LLM from provider-specific config.
// The ServiceRegistry is available for providers that need infrastructure services.
type ModelProvider func(config orchestration.ModelProviderConfig, svcReg ServiceRegistry) (model.LLM, error)

var (
	modelProvidersMu sync.RWMutex
	modelProviders   = make(map[string]ModelProvider)
)

// RegisterModelProvider registers a named model provider (e.g., "openai", "anthropic").
func RegisterModelProvider(name string, provider ModelProvider) {
	modelProvidersMu.Lock()
	defer modelProvidersMu.Unlock()
	modelProviders[name] = provider
}

// GetModelProvider returns the model provider by name.
func GetModelProvider(name string) (ModelProvider, error) {
	modelProvidersMu.RLock()
	defer modelProvidersMu.RUnlock()
	p, ok := modelProviders[name]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: unknown model provider %q", name)
	}
	return p, nil
}

// ---- Tool Provider ----

// ToolProvider constructs a tool.Tool from provider-specific config.
// The ServiceRegistry is available for providers that need infrastructure services
// (e.g., filegentool needs an artifact.Service).
type ToolProvider func(config map[string]any, svcReg ServiceRegistry) (tool.Tool, error)

var (
	toolProvidersMu sync.RWMutex
	toolProviders   = make(map[string]ToolProvider)
)

// RegisterToolProvider registers a named tool provider (e.g., "filegentool").
func RegisterToolProvider(name string, provider ToolProvider) {
	toolProvidersMu.Lock()
	defer toolProvidersMu.Unlock()
	toolProviders[name] = provider
}

// GetToolProvider returns the tool provider by name.
func GetToolProvider(name string) (ToolProvider, error) {
	toolProvidersMu.RLock()
	defer toolProvidersMu.RUnlock()
	p, ok := toolProviders[name]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: unknown tool provider %q", name)
	}
	return p, nil
}

// ---- Callback Provider ----

// CallbackProvider constructs BeforeAgent and/or AfterAgent callbacks from
// provider-specific config. Either callback may be nil if the provider only
// produces one type.
type CallbackProvider func(config map[string]any, svcReg ServiceRegistry) (
	beforeAgent adkagent.BeforeAgentCallback,
	afterAgent adkagent.AfterAgentCallback,
	err error,
)

var (
	callbackProvidersMu sync.RWMutex
	callbackProviders   = make(map[string]CallbackProvider)
)

// RegisterCallbackProvider registers a named callback provider (e.g., "conditional_skip").
func RegisterCallbackProvider(name string, provider CallbackProvider) {
	callbackProvidersMu.Lock()
	defer callbackProvidersMu.Unlock()
	callbackProviders[name] = provider
}

// GetCallbackProvider returns the callback provider by name.
func GetCallbackProvider(name string) (CallbackProvider, error) {
	callbackProvidersMu.RLock()
	defer callbackProvidersMu.RUnlock()
	p, ok := callbackProviders[name]
	if !ok {
		return nil, fmt.Errorf("orchestration/registry: unknown callback provider %q", name)
	}
	return p, nil
}
