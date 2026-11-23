package harness

import (
	"encoding/hex"

	"github.com/mindfire/zygote/trace"
)

// ReplayHandler bridges the JSON-RPC interface to a trace.RunReplayer.
type ReplayHandler struct {
	r *trace.RunReplayer
}

// NewReplayHandler creates a new handler for replaying.
func NewReplayHandler(r *trace.RunReplayer) *ReplayHandler {
	return &ReplayHandler{r: r}
}

// Handshake validates version.
func (h *ReplayHandler) Handshake(_ int) (int, error) {
	return 1, nil // we support v1
}

// Effect fetches the recorded effect, ignoring the requested value.
func (h *ReplayHandler) Effect(op, key string, _ []byte) ([]byte, error) {
	return h.EffectWithActor("", op, key, nil)
}

// EffectWithActor fetches the recorded effect for a specific actor.
func (h *ReplayHandler) EffectWithActor(actor, op, key string, _ []byte) ([]byte, error) {
	return h.r.Replayer().ReplayWithActor(actor, op, key)
}

// Step verifies the agent's world against the recording.
func (h *ReplayHandler) Step(name string) (*StepResult, error) {
	return h.StepWithParent("", name, nil)
}

// StepWithActor verifies the agent's world against the recording for a specific actor.
func (h *ReplayHandler) StepWithActor(actor, name string) (*StepResult, error) {
	return h.StepWithParent(actor, name, nil)
}

// StepWithParent verifies both top-level and nested step world hashes, actor, and parent ID.
func (h *ReplayHandler) StepWithParent(actor, name string, parentID *int) (*StepResult, error) {
	chk, err := h.r.StepWithParent(name, actor, parentID)
	if err != nil {
		return nil, err
	}

	return &StepResult{
		Match:    chk.Match,
		Expected: hex.EncodeToString(chk.Expected.Root[:]),
		Actual:   hex.EncodeToString(chk.Actual.Root[:]),
	}, nil
}
