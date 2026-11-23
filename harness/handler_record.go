package harness

import (
	"encoding/hex"

	"github.com/mindfire/zygote/trace"
)

// RecordHandler bridges the JSON-RPC interface to a trace.RunRecorder.
type RecordHandler struct {
	r *trace.RunRecorder
}

// NewRecordHandler creates a new handler for recording.
func NewRecordHandler(r *trace.RunRecorder) *RecordHandler {
	return &RecordHandler{r: r}
}

// Handshake validates version.
func (h *RecordHandler) Handshake(_ int) (int, error) {
	return 1, nil // we support v1
}

// Effect records the effect and returns the value back to the agent.
func (h *RecordHandler) Effect(op, key string, value []byte) ([]byte, error) {
	return h.EffectWithActor("", op, key, value)
}

// EffectWithActor records the effect for a specific actor and returns the value.
func (h *RecordHandler) EffectWithActor(actor, op, key string, value []byte) ([]byte, error) {
	return h.r.Recorder().RecordWithActor(actor, op, key, func() ([]byte, error) {
		return value, nil
	})
}

// Step captures the world state boundary.
func (h *RecordHandler) Step(name string) (*StepResult, error) {
	return h.StepWithActor("", name)
}

// StepWithActor captures the world state boundary for a specific actor.
func (h *RecordHandler) StepWithActor(actor, name string) (*StepResult, error) {
	h.r.StepWithActor(name, actor)

	// Record doesn't match against anything, just returns success.
	// We return the actual hash of the step we just took.
	worldHash := h.r.World().Snapshot().Root
	hashStr := hex.EncodeToString(worldHash[:])

	return &StepResult{
		Match:    true,
		Expected: hashStr,
		Actual:   hashStr,
	}, nil
}
