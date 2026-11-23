package harness

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

type mockHandler struct {
	mu       sync.Mutex
	opCalled bool
}

func (m *mockHandler) Handshake(version int) (int, error) { return version, nil }
func (m *mockHandler) Effect(_, _ string, value []byte) ([]byte, error) {
	m.mu.Lock()
	m.opCalled = true
	m.mu.Unlock()
	return value, nil
}
func (m *mockHandler) Step(_ string) (*StepResult, error) {
	return &StepResult{Match: true}, nil
}

func TestServerGarbageLog(t *testing.T) {
	// A stream with raw agent logs mixed with JSON-RPC
	input := `Loading AI models...
{"jsonrpc": "2.0", "method": "effect", "params": {"op": "model", "key": "prompt-1", "value": "SGVsbG8="}, "id": 1}
Agent finished.
`
	in := strings.NewReader(input)
	var out bytes.Buffer

	h := &mockHandler{}
	srv := NewServer(in, &out, h)

	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	h.mu.Lock()
	called := h.opCalled
	h.mu.Unlock()

	if !called {
		t.Fatal("Expected Effect handler to be called despite garbage logs")
	}

	if !strings.Contains(out.String(), "result") {
		t.Errorf("Expected valid JSON-RPC response, got: %s", out.String())
	}
}

type mockActorHandler struct {
	mockHandler
	mu            sync.Mutex
	lastActor     string
	lastStepActor string
}

func (m *mockActorHandler) EffectWithActor(actor, op, key string, value []byte) ([]byte, error) {
	m.mu.Lock()
	m.lastActor = actor
	m.mu.Unlock()
	return m.Effect(op, key, value)
}

func (m *mockActorHandler) StepWithActor(actor, name string) (*StepResult, error) {
	m.mu.Lock()
	m.lastStepActor = actor
	m.mu.Unlock()
	return m.Step(name)
}

func TestServerMultiActor(t *testing.T) {
	input := `{"jsonrpc": "2.0", "method": "effect", "params": {"op": "model", "key": "k1", "value": "SGVsbG8=", "actor": "worker-1"}, "id": 1}
{"jsonrpc": "2.0", "method": "effect", "params": {"op": "tool", "key": "k2", "value": "V29ybGQ=", "thread_id": "thread-2"}, "id": 2}
{"jsonrpc": "2.0", "method": "step", "params": {"name": "step-A", "actor": "worker-1"}, "id": 3}
`
	in := strings.NewReader(input)
	var out bytes.Buffer

	h := &mockActorHandler{}
	srv := NewServer(in, &out, h)

	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("Expected 3 responses, got %d: %v", len(lines), lines)
	}
}
