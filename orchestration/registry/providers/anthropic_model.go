package providers

import (
	"os"

	"github.com/UnderTreeTech/adk-go/model/anthropic"
	"github.com/UnderTreeTech/adk-go/orchestration"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
	"google.golang.org/adk/model"
)

func init() {
	registry.RegisterModelProvider("anthropic", anthropicModelProvider)
}

// anthropicModelProvider creates an Anthropic model from config.
// It reads the API key from the environment variable specified in
// ModelProviderConfig.APIKeyEnv. The Anthropic SDK also falls back to
// ANTHROPIC_API_KEY if no key is provided.
func anthropicModelProvider(config orchestration.ModelProviderConfig, svcReg registry.ServiceRegistry) (model.LLM, error) {
	apiKey := ""
	if config.APIKeyEnv != "" {
		apiKey = os.Getenv(config.APIKeyEnv)
	}

	baseURL := ""
	if config.BaseURLEnv != "" {
		baseURL = os.Getenv(config.BaseURLEnv)
	}

	return anthropic.New(anthropic.Config{
		APIKey:               apiKey,
		BaseURL:              baseURL,
		ModelName:            config.ModelName,
		MaxOutputTokens:      config.MaxOutputTokens,
		ThinkingBudgetTokens: config.ThinkingBudgetTokens,
	}), nil
}
