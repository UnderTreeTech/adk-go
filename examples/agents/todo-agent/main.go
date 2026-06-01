// Package main demonstrates a "Todo Agent" that:
//  1. Accepts a user task description
//  2. Uses an LLM to analyze the task and create a todo list
//  3. Iteratively executes each todo item, checking it off upon completion
//  4. Continues until all items are done
//
// This example showcases how to build a self-driving agent loop using
// function tools for state management (todo list CRUD) and an LLM for
// planning & execution.
//
// Usage:
//
//	go run ./examples/todo-agent --run "帮我规划一次从上海到北京的旅行"
//
// Environment variables:
//
//	OPENAI_API_KEY   – LLM API key
//	OPENAI_BASE_URL  – LLM base URL
//	MODEL_NAME       – model identifier
//	JAEGER_ENDPOINT  – (optional) Jaeger OTLP endpoint
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/UnderTreeTech/adk-go/agent"
	genaiopenai "github.com/UnderTreeTech/adk-go/model/openai"
	//"github.com/UnderTreeTech/adk-go/plugin/trace/jaeger"
	"github.com/UnderTreeTech/adk-go/plugin/trace/langfuse"
	"github.com/UnderTreeTech/adk-go/prompt"
	"github.com/UnderTreeTech/waterdrop/pkg/log"
	adkAgent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ---------------------------------------------------------------------------
// Todo Item data model
// ---------------------------------------------------------------------------

// TodoStatus represents the status of a todo item.
type TodoStatus string

const (
	// StatusPending means the todo item has not been started.
	StatusPending TodoStatus = "pending"
	// StatusInProgress means the todo item is currently being worked on.
	StatusInProgress TodoStatus = "in_progress"
	// StatusDone means the todo item has been completed.
	StatusDone TodoStatus = "done"
)

// TodoItem represents a single item in the todo list.
type TodoItem struct {
	ID     int        `json:"id"`
	Title  string     `json:"title"`
	Status TodoStatus `json:"status"`
	Result string     `json:"result,omitempty"`
}

// ---------------------------------------------------------------------------
// In-memory Todo Store (thread-safe)
// ---------------------------------------------------------------------------

// TodoStore manages the todo list state. It is safe for concurrent access.
type TodoStore struct {
	mu    sync.RWMutex
	items []TodoItem
}

// globalStore is the singleton todo store used by all tool handlers.
var globalStore = &TodoStore{}

// CreateList replaces the current todo list with the given items.
func (s *TodoStore) CreateList(titles []string) []TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = make([]TodoItem, len(titles))
	for i, t := range titles {
		s.items[i] = TodoItem{
			ID:     i + 1,
			Title:  t,
			Status: StatusPending,
		}
	}
	return s.snapshot()
}

// UpdateItem sets the status and optional result for a given item ID.
func (s *TodoStore) UpdateItem(id int, status TodoStatus, result string) (*TodoItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].Status = status
			if result != "" {
				s.items[i].Result = result
			}
			item := s.items[i]
			return &item, nil
		}
	}
	return nil, fmt.Errorf("todo item with id %d not found", id)
}

// List returns the current snapshot of all items.
func (s *TodoStore) List() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot()
}

// AllDone returns true if every item is in StatusDone.
func (s *TodoStore) AllDone() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.items) == 0 {
		return false
	}
	for _, item := range s.items {
		if item.Status != StatusDone {
			return false
		}
	}
	return true
}

// snapshot returns a copy of the items slice (caller must hold lock).
func (s *TodoStore) snapshot() []TodoItem {
	cp := make([]TodoItem, len(s.items))
	copy(cp, s.items)
	return cp
}

// ---------------------------------------------------------------------------
// Function Tool: create_todo_list
// ---------------------------------------------------------------------------

// CreateTodoListArgs is the argument schema for the create_todo_list tool.
type CreateTodoListArgs struct {
	Items []string `json:"items" jsonschema:"A list of todo item titles derived from the user's task. Each title should be a clear and actionable step."`
}

// NewCreateTodoListTool returns a tool that creates a new todo list.
func NewCreateTodoListTool() (tool.Tool, error) {
	handler := func(ctx tool.Context, args CreateTodoListArgs) (map[string]any, error) {
		if len(args.Items) == 0 {
			return map[string]any{"error": "items list cannot be empty"}, nil
		}
		items := globalStore.CreateList(args.Items)

		// Build a formatted checklist for user display
		display := formatTodoChecklist(items)

		return map[string]any{
			"status":           "created",
			"todo_list":        items,
			"total_steps":      len(items),
			"display_for_user": display,
			"message":          fmt.Sprintf("Successfully created %d todo items. Please present the full plan to the user FIRST before executing.", len(items)),
		}, nil
	}
	return functiontool.New(
		functiontool.Config{
			Name: "create_todo_list",
			Description: `Create a new todo list from user's task description.
Analyze the user's input, break it down into clear, actionable steps,
and create a todo list. Each item should be a concrete step.

Args:
    items: A list of todo item title strings. Each should be one clear actionable step.
Returns:
    The created todo list with IDs and status, plus a formatted display string for the user.`,
		},
		handler,
	)
}

// ---------------------------------------------------------------------------
// Function Tool: update_todo_item
// ---------------------------------------------------------------------------

// UpdateTodoItemArgs is the argument schema for the update_todo_item tool.
type UpdateTodoItemArgs struct {
	ID     int    `json:"id" jsonschema:"The ID of the todo item to update"`
	Status string `json:"status" jsonschema:"New status: pending, in_progress, or done"`
	Result string `json:"result" jsonschema:"The execution result or output for this todo item. Required when marking as done"`
}

// NewUpdateTodoItemTool returns a tool that updates a todo item's status.
func NewUpdateTodoItemTool() (tool.Tool, error) {
	handler := func(ctx tool.Context, args UpdateTodoItemArgs) (map[string]any, error) {
		status := TodoStatus(args.Status)
		switch status {
		case StatusPending, StatusInProgress, StatusDone:
			// valid
		default:
			return map[string]any{"error": fmt.Sprintf("invalid status: %s, must be one of: pending, in_progress, done", args.Status)}, nil
		}

		item, err := globalStore.UpdateItem(args.ID, status, args.Result)
		if err != nil {
			return map[string]any{"error": err.Error()}, nil
		}

		// Build full progress snapshot for the LLM to present to user
		allItems := globalStore.List()
		allDone := globalStore.AllDone()
		progress := formatTodoChecklist(allItems)

		return map[string]any{
			"status":           "updated",
			"updated_item":     item,
			"all_done":         allDone,
			"progress_display": progress,
			"message":          fmt.Sprintf("Todo #%d updated to '%s'. Present the progress to the user.", item.ID, item.Status),
		}, nil
	}
	return functiontool.New(
		functiontool.Config{
			Name: "update_todo_item",
			Description: `Update the status of a specific todo item.

Use this tool to:
- Mark an item as "in_progress" when you start working on it
- Mark an item as "done" when you have completed it (must provide result)

Args:
    id:     The ID of the todo item (integer).
    status: New status string — one of "pending", "in_progress", "done".
    result: The execution result or summary for this step. Required when status is "done".
Returns:
    The updated item, progress display, and whether all items are now done.`,
		},
		handler,
	)
}

// ---------------------------------------------------------------------------
// Function Tool: get_todo_list
// ---------------------------------------------------------------------------

// GetTodoListArgs is the argument schema for the get_todo_list tool.
type GetTodoListArgs struct{}

// NewGetTodoListTool returns a tool that retrieves the current todo list.
func NewGetTodoListTool() (tool.Tool, error) {
	handler := func(ctx tool.Context, args GetTodoListArgs) (map[string]any, error) {
		items := globalStore.List()
		if len(items) == 0 {
			return map[string]any{
				"todo_list": []TodoItem{},
				"message":   "No todo list exists yet. Use create_todo_list first.",
			}, nil
		}

		pending := 0
		inProgress := 0
		done := 0
		for _, item := range items {
			switch item.Status {
			case StatusPending:
				pending++
			case StatusInProgress:
				inProgress++
			case StatusDone:
				done++
			}
		}

		return map[string]any{
			"todo_list": items,
			"summary": map[string]int{
				"total":       len(items),
				"pending":     pending,
				"in_progress": inProgress,
				"done":        done,
			},
			"all_done": globalStore.AllDone(),
		}, nil
	}
	return functiontool.New(
		functiontool.Config{
			Name: "get_todo_list",
			Description: `Get the current todo list with all items and their statuses.

Returns:
    The full todo list, a summary of counts by status, and whether all are done.`,
		},
		handler,
	)
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

const todoAgentInstruction = `You are a Todo Agent — an intelligent task planner and executor.
Your responses should always be in the SAME LANGUAGE as the user's input.

## Your Workflow

### Phase 1: Planning (规划阶段)
When the user gives you a task or goal:
1. Analyze the task carefully
2. Break it down into clear, actionable, ordered steps (typically 3-8 steps)
3. Call the "create_todo_list" tool with these steps
4. **IMPORTANT**: After creating the todo list, you MUST present the FULL plan to the user in a clear checklist format:
   - Show all steps numbered with checkboxes
   - Briefly explain what you will do in each step
   - Ask the user to confirm or say "I will now start executing the plan step by step"
   - Example output format:
     📋 **Task Plan**
     Here's my plan to accomplish your task:
     ⬜ Step 1: ...
     ⬜ Step 2: ...
     ⬜ Step 3: ...
     I will now execute these steps one by one.

### Phase 2: Execution (执行阶段)
After presenting the plan, execute each item one by one. For EACH step:

1. **Announce**: Tell the user which step you are starting (e.g., "🔄 Starting Step 2: ...")
2. **Mark in_progress**: Call "update_todo_item" with status "in_progress"
3. **Execute**: Actually work on the task — think, reason, provide substantive content
4. **Mark done**: Call "update_todo_item" with status "done" and a clear result summary
5. **Report progress**: After each step is done, present to the user:
   - The result of the current step
   - Updated progress checklist showing ✅ for done, 🔄 for in progress, ⬜ for pending
   - What comes next

Example after completing step 2 of 4:
   ✅ Step 1: xxx — Done: [brief result]
   ✅ Step 2: xxx — Done: [brief result]  ← just completed
   ⬜ Step 3: xxx
   ⬜ Step 4: xxx
   ➡️ Moving to Step 3...

### Phase 3: Summary (总结阶段)
Once all items are done (all_done == true):
1. Call "get_todo_list" one final time
2. Present a comprehensive final summary:
   - Show all steps with ✅ and their results
   - Provide a brief overall conclusion
   - Example:
     🎉 **All tasks completed!**
     ✅ Step 1: ... → [result]
     ✅ Step 2: ... → [result]
     ✅ Step 3: ... → [result]
     **Summary**: [overall conclusion]

## Important Rules
- ALWAYS create the todo list FIRST and present it to the user before executing anything
- Execute items IN ORDER (by ID), never skip ahead
- ALWAYS show progress with the checklist after EACH step completes
- ALWAYS provide a meaningful "result" when marking an item as "done"
- If a step requires information from a previous step, reference the previous result
- Be thorough and provide real, useful content for each step
- The progress display should help the user understand where we are in the overall plan
- Use emojis (✅ ⬜ 🔄 ➡️) to make progress visually clear`

func main() {
	defer log.New(nil).Sync()
	ctx := context.Background()

	// Setup Jaeger tracing (optional)
	//jaegerCfg, jaegerShutdown, err := jaeger.Setup(&jaeger.Config{
	//	Endpoint:    getEnvOrDefault("JAEGER_ENDPOINT", "http://localhost:4318/v1/traces"),
	//	ServiceName: getEnvOrDefault("JAEGER_SERVICE_NAME", "todo-agent"),
	//	Environment: getEnvOrDefault("JAEGER_ENVIRONMENT", "development"),
	//	Insecure:    true,
	//})
	//if err != nil {
	//	fmt.Printf("setup jaeger failed: %v\n", err)
	//	return
	//}
	//defer jaegerShutdown(ctx)

	langfuseCfg, langfuseShutdown, _ := langfuse.Setup(&langfuse.Config{
		Host:      getEnvOrDefault("LANGFUSE_HOST", ""),
		PublicKey: getEnvOrDefault("LANGFUSE_PUBLIC_KEY", ""),
		SecretKey: getEnvOrDefault("LANGFUSE_SECRET_KEY", ""),
	})
	defer langfuseShutdown(ctx)

	// Setup LLM
	extras := make(map[string]any)
	extras["thinking"] = map[string]any{
		"type": "disabled",
	}
	llmModel := genaiopenai.New(&genaiopenai.Config{
		APIKey:    getEnvOrDefault("OPENAI_API_KEY", ""),
		BaseURL:   getEnvOrDefault("OPENAI_BASE_URL", ""),
		ModelName: getEnvOrDefault("MODEL_NAME", ""),
		ExtraBody: map[string]any{
			"extra_body": extras,
		},
	})

	// Create tools
	createTodoTool, err := NewCreateTodoListTool()
	if err != nil {
		fmt.Printf("create CreateTodoListTool failed: %v\n", err)
		return
	}

	updateTodoTool, err := NewUpdateTodoItemTool()
	if err != nil {
		fmt.Printf("create UpdateTodoItemTool failed: %v\n", err)
		return
	}

	getTodoTool, err := NewGetTodoListTool()
	if err != nil {
		fmt.Printf("create GetTodoListTool failed: %v\n", err)
		return
	}

	// Build the agent
	a, err := agent.NewLLMAgent(agent.Config{
		LLMAgentConfig: llmagent.Config{
			Name:              "todo_agent",
			Model:             llmModel,
			Description:       "An intelligent agent that breaks down tasks into a todo list and executes them step by step, checking off each item upon completion.",
			GlobalInstruction: prompt.GlobalInstruction,
			Instruction:       todoAgentInstruction,
			Tools: []tool.Tool{
				createTodoTool,
				updateTodoTool,
				getTodoTool,
			},
		},
	})
	if err != nil {
		panic(fmt.Sprintf("Failed to create todo agent: %v", err))
	}

	// Assemble runner-level plugins: Langfuse trace
	config := &launcher.Config{
		AgentLoader: adkAgent.NewSingleLoader(a),
		PluginConfig: runner.PluginConfig{
			Plugins: langfuseCfg.Plugins,
		},
	}

	l := full.NewLauncher()
	if err = l.Execute(ctx, config, os.Args[1:]); err != nil {
		panic(fmt.Sprintf("Run failed: %v\n\n%s", err, l.CommandLineSyntax()))
	}
}

// getEnvOrDefault returns the environment variable value or a fallback default.
func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// formatTodoChecklist renders the todo list as a user-friendly checklist string
// that the LLM can directly present to the user.
func formatTodoChecklist(items []TodoItem) string {
	if len(items) == 0 {
		return "(empty)"
	}
	var sb strings.Builder
	done := 0
	for _, item := range items {
		var icon string
		switch item.Status {
		case StatusDone:
			icon = "✅"
			done++
		case StatusInProgress:
			icon = "🔄"
		default:
			icon = "⬜"
		}
		sb.WriteString(fmt.Sprintf("%s Step %d: %s", icon, item.ID, item.Title))
		if item.Result != "" {
			sb.WriteString(fmt.Sprintf(" → %s", item.Result))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("\n📊 Progress: %d/%d completed", done, len(items)))
	return sb.String()
}
