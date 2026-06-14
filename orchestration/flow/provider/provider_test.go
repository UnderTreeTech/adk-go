package provider

import (
	"context"
	"fmt"
	"iter"
	"testing"

	adkagent "google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// mockLLM is a minimal mock model.LLM for creating real agents.
type mockLLM struct {
	name string
}

func (m *mockLLM) Name() string { return m.name }
func (m *mockLLM) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		yield(&adkmodel.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "mock response"}}}}, nil)
	}
}

// newTestAgent creates a real adkagent.Agent with a mock model for testing.
func newTestAgent(name string) adkagent.Agent {
	ag, err := adkagent.New(adkagent.Config{
		Name: name,
		Run: func(_ adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {}
		},
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create test agent: %v", err))
	}
	return ag
}

func TestMapAgentProviderRegisterAndGet(t *testing.T) {
	p := NewMapAgentProvider()

	agent1 := newTestAgent("agent1")
	agent2 := newTestAgent("agent2")

	if err := p.Register("block1", agent1); err != nil {
		t.Fatalf("Register block1: %v", err)
	}
	if err := p.Register("block2", agent2); err != nil {
		t.Fatalf("Register block2: %v", err)
	}

	// Get should return the registered agents
	got1, err := p.Get("block1")
	if err != nil {
		t.Fatalf("Get block1: %v", err)
	}
	if got1.Name() != "agent1" {
		t.Errorf("Get block1 = %q, want %q", got1.Name(), "agent1")
	}

	got2, err := p.Get("block2")
	if err != nil {
		t.Fatalf("Get block2: %v", err)
	}
	if got2.Name() != "agent2" {
		t.Errorf("Get block2 = %q, want %q", got2.Name(), "agent2")
	}
}

func TestMapAgentProviderDuplicate(t *testing.T) {
	p := NewMapAgentProvider()
	agent1 := newTestAgent("agent1")

	if err := p.Register("block1", agent1); err != nil {
		t.Fatalf("Register block1: %v", err)
	}

	// Duplicate registration should fail
	if err := p.Register("block1", agent1); err == nil {
		t.Fatal("Duplicate Register should fail")
	}
}

func TestMapAgentProviderGetMissing(t *testing.T) {
	p := NewMapAgentProvider()

	// Get for unregistered block should fail
	_, err := p.Get("nonexistent")
	if err == nil {
		t.Fatal("Get nonexistent should fail")
	}
}

func TestMapAgentProviderBlockIDs(t *testing.T) {
	p := NewMapAgentProvider()
	p.Register("c", newTestAgent("c"))
	p.Register("a", newTestAgent("a"))
	p.Register("b", newTestAgent("b"))

	ids := p.BlockIDs()
	if len(ids) != 3 {
		t.Fatalf("len(BlockIDs) = %d, want 3", len(ids))
	}
	// Should be sorted
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("BlockIDs = %v, want [a, b, c]", ids)
	}
}

func TestAgentProviderFunc(t *testing.T) {
	fn := AgentProviderFunc(func(blockID string) (adkagent.Agent, error) {
		if blockID == "known" {
			return newTestAgent("known_agent"), nil
		}
		return nil, fmt.Errorf("unknown block %q", blockID)
	})

	agent, err := fn.Get("known")
	if err != nil {
		t.Fatalf("Get known: %v", err)
	}
	if agent.Name() != "known_agent" {
		t.Errorf("Get known = %q, want %q", agent.Name(), "known_agent")
	}

	_, err = fn.Get("unknown")
	if err == nil {
		t.Fatal("Get unknown should fail")
	}

	ids := fn.BlockIDs()
	if ids != nil {
		t.Errorf("BlockIDs = %v, want nil", ids)
	}
}
