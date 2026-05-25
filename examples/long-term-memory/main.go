// Long-Term Memory Example
//
// This example demonstrates how to use PostgreSQL with pgvector for long-term memory
// in an ADK agent. The agent can search and save memories across sessions.
//
// Requirements:
// - PostgreSQL with pgvector extension
// - Ollama running locally with nomic-embed-text model
//
// Run PostgreSQL:
//   docker run -d --name postgres -e POSTGRES_PASSWORD=postgres -p 5432:5432 pgvector/pgvector:pg16
//
// Run Ollama:
//   ollama pull nomic-embed-text

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/UnderTreeTech/adk-go/agent"
	adkAgent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	genaiopenai "github.com/UnderTreeTech/adk-go/genai/openai"
	memorypostgres "github.com/UnderTreeTech/adk-go/memory/postgres"
	memorytools "github.com/UnderTreeTech/adk-go/tools/memory"
)

const (
	appName = "long_term_memory_example"
	userID  = "demo_user"
)

func main() {
	ctx := context.Background()

	// Configure OpenAI-compatible model (Ollama)
	llmModel := getOpenAIModel()

	// Initialize PostgreSQL memory service with embeddings for semantic search
	pgMemoryService, err := memorypostgres.NewPostgresMemoryService(ctx, memorypostgres.PostgresMemoryServiceConfig{
		ConnString: getEnvOrDefault("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"),
		EmbeddingModel: memorypostgres.NewOpenAICompatibleEmbedding(memorypostgres.OpenAICompatibleEmbeddingConfig{
			BaseURL: getEnvOrDefault("EMBEDDING_BASE_URL", "http://localhost:11434/v1"),
			Model:   getEnvOrDefault("EMBEDDING_MODEL", "nomic-embed-text"),
		}),
	})
	if err != nil {
		log.Fatalf("Failed to create Postgres memory service: %v", err)
	}
	defer pgMemoryService.Close()

	// Use in-memory session service (long-term memory persists in PostgreSQL)
	sessionService := session.InMemoryService()

	// Create session
	sessResp, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: appName,
		UserID:  userID,
	})
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}

	// Create memory toolset for the agent
	memoryToolset, err := memorytools.NewToolset(memorytools.ToolsetConfig{
		MemoryService: pgMemoryService,
		AppName:       appName,
	})
	if err != nil {
		log.Fatalf("Failed to create memory toolset: %v", err)
	}

	// Create agent with memory tools
	rootAgent, err := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:        "memory_agent",
			Model:       llmModel,
			Description: "An agent with long-term memory capabilities.",
			Instruction: `You are a helpful assistant with access to long-term memory.

You have access to these memory tools:
- search_memory: Search for information from past conversations
- save_to_memory: Save important information for future recall

Guidelines:
1. When a user shares preferences, facts, or asks you to remember something, use save_to_memory
2. When a user asks about something they told you before, use search_memory first
3. Proactively save important information the user shares
4. Be explicit about what you're remembering or recalling`,
			Toolsets: []tool.Toolset{
				memoryToolset,
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// Create runner with memory service
	runnr, err := runner.New(runner.Config{
		AppName:        appName,
		Agent:          rootAgent.Agent,
		SessionService: sessionService,
		MemoryService:  pgMemoryService,
		PluginConfig:   rootAgent.PluginConfig,
	})
	if err != nil {
		log.Fatalf("Failed to create runner: %v", err)
	}

	// Example interaction: Save and recall preferences
	interactions := []string{
		"My favorite programming language is Go and I prefer using PostgreSQL for databases. Please remember this.",
		"What are my technology preferences?",
	}

	for i, userInput := range interactions {
		fmt.Printf("\n=== Interaction %d ===\n", i+1)
		fmt.Printf("User: %s\n", userInput)

		response := runAgent(ctx, runnr, sessResp.Session.ID(), userInput)
		fmt.Printf("Agent: %s\n", response)
	}
}

func runAgent(ctx context.Context, runnr *runner.Runner, sessionID string, input string) string {
	userMsg := genai.NewContentFromText(input, genai.RoleUser)

	var responseText string
	for event, err := range runnr.Run(ctx, userID, sessionID, userMsg, adkAgent.RunConfig{}) {
		if err != nil {
			log.Printf("Error: %v", err)
			break
		}
		if event.ErrorCode != "" {
			log.Printf("Event error: %s - %s", event.ErrorCode, event.ErrorMessage)
			break
		}
		if event.Content != nil && len(event.Content.Parts) > 0 {
			responseText += event.Content.Parts[0].Text
		}
	}

	return responseText
}

func getOpenAIModel() model.LLM {
	return genaiopenai.New(&genaiopenai.Config{
		APIKey:    os.Getenv("OPENAI_API_KEY"),
		BaseURL:   getEnvOrDefault("OPENAI_BASE_URL", "http://localhost:11434/v1"),
		ModelName: getEnvOrDefault("MODEL_NAME", "qwen3:8b"),
	})
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
