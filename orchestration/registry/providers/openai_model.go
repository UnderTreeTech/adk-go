package providers

import (
	"os"

	"github.com/UnderTreeTech/adk-go/model/openai"
	"github.com/UnderTreeTech/adk-go/orchestration"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
	"google.golang.org/adk/model"
)

func init() {
	registry.RegisterModelProvider("openai", openaiModelProvider)
}

// openaiModelProvider creates an OpenAI-compatible model from config.
// It reads the API key and base URL from environment variables specified
// in ModelProviderConfig.APIKeyEnv and ModelProviderConfig.BaseURLEnv.
func openaiModelProvider(config orchestration.ModelProviderConfig, svcReg registry.ServiceRegistry) (model.LLM, error) {
	apiKey := ""
	if config.APIKeyEnv != "" {
		apiKey = os.Getenv(config.APIKeyEnv)
	}

	baseURL := ""
	if config.BaseURLEnv != "" {
		baseURL = os.Getenv(config.BaseURLEnv)
	}

	return openai.New(&openai.Config{
		ModelName: config.ModelName,
		APIKey:    apiKey,
		BaseURL:   baseURL,
		ExtraBody: config.ExtraBody,
	}), nil
}
