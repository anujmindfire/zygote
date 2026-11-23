package trace

import (
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/mindfire/zygote/journal"
	"github.com/mindfire/zygote/vfs"
)

// RunRecorder orchestrates the agent's live filesystem (World) and its
// nondeterministic effects (Journal), allowing discrete Steps to be captured.
type RunRecorder struct {
	world    *vfs.World
	journal  *journal.Recorder
	steps    []Step
	meta     map[string]string
	stepBase int
}

// Record begins a new trace tracking session on top of a store.
func Record(store vfs.Store) *RunRecorder {
	return &RunRecorder{
		world:   vfs.NewWorld(store),
		journal: journal.NewRecorder(),
		steps:   make([]Step, 0),
		meta:    make(map[string]string),
	}
}

// Set stores arbitrary string metadata for the run (e.g. model version, commit).
func (r *RunRecorder) Set(key, value string) {
	r.meta[key] = value
}

// World returns the live Virtual File System the agent is currently editing.
func (r *RunRecorder) World() *vfs.World {
	return r.world
}

// Recorder returns the journal for logging nondeterministic effects.
func (r *RunRecorder) Recorder() *journal.Recorder {
	return r.journal
}

// Step captures the current world state and effect count as a named boundary.
func (r *RunRecorder) Step(name string) {
	r.StepWithParent(name, "", nil)
}

// StepWithActor captures the current world state and effect count as a named boundary for an actor.
func (r *RunRecorder) StepWithActor(name, actor string) {
	r.StepWithParent(name, actor, nil)
}

// StepWithParent captures the current world state and effect count as a named boundary,
// attributing it to an actor and an optional parent step ID (FR-3.10).
func (r *RunRecorder) StepWithParent(name, actor string, parentID *int) {
	snap := r.world.Snapshot()
	effs := len(r.journal.Entries())

	step := Step{
		N:        r.stepBase,
		Name:     name,
		Root:     hex.EncodeToString(snap.Root[:]),
		Effects:  effs,
		Actor:    actor,
		ParentID: parentID,
	}

	r.steps = append(r.steps, step)
	r.stepBase++
}

// Recording returns the finalized manifest of all steps and effects.
// Steps are structured into a hierarchical step tree (FR-3.10).
func (r *RunRecorder) Recording() Recording {
	return Recording{
		Version:       1,
		HashAlgorithm: "blake3",
		Producer:      "zygote-cli",
		Meta:          r.meta,
		Steps:         BuildStepTree(r.steps),
		Effects:       r.journal.Entries(),
	}
}

// BuildStepTree builds a hierarchical step tree from a slice of steps.
// Top-level steps (ParentID == nil) are returned with their nested descendants
// populated in Children recursively. If all steps are already top-level, it returns them as-is.
func BuildStepTree(steps []Step) []Step {
	if len(steps) == 0 {
		return nil
	}

	hasParent := false
	for _, s := range steps {
		if s.ParentID != nil {
			hasParent = true
			break
		}
	}
	if !hasParent {
		out := make([]Step, len(steps))
		copy(out, steps)
		return out
	}

	// Group children by parent ID
	childrenOf := make(map[int][]Step)
	byID := make(map[int]Step, len(steps))
	for _, s := range steps {
		sCopy := s
		sCopy.Children = nil
		byID[s.N] = sCopy
		if s.ParentID != nil {
			childrenOf[*s.ParentID] = append(childrenOf[*s.ParentID], sCopy)
		}
	}

	var attachChildren func(s Step) Step
	attachChildren = func(s Step) Step {
		directChildren := childrenOf[s.N]
		if len(directChildren) == 0 {
			s.Children = nil
			return s
		}
		s.Children = make([]Step, len(directChildren))
		for i, child := range directChildren {
			s.Children[i] = attachChildren(child)
		}
		return s
	}

	var rootSteps []Step
	for _, s := range steps {
		if s.ParentID == nil {
			rootSteps = append(rootSteps, attachChildren(s))
		} else if _, ok := byID[*s.ParentID]; !ok {
			// Orphan step whose parent was not found: preserve at root
			rootSteps = append(rootSteps, attachChildren(s))
		}
	}

	return rootSteps
}

// FlattenSteps extracts all steps from a hierarchical tree in chronological order by N.
func FlattenSteps(steps []Step) []Step {
	var out []Step
	var walk func(s Step)
	walk = func(s Step) {
		stepCopy := s
		stepCopy.Children = nil
		out = append(out, stepCopy)
		for _, child := range s.Children {
			walk(child)
		}
	}
	for _, s := range steps {
		walk(s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].N < out[j].N
	})
	return out
}

// AllSteps returns all steps in the recording flattened in chronological order.
func (r Recording) AllSteps() []Step {
	return FlattenSteps(r.Steps)
}

// RunReplayer orchestrates the replay of a run, checking step boundaries against a Bundle.
type RunReplayer struct {
	bundle   *Bundle
	replayer *journal.Replayer
	world    *vfs.World
	allSteps []Step
	stepIdx  int
}

// Replay initializes a RunReplayer from a Bundle.
func Replay(store vfs.Store, bundle *Bundle) (*RunReplayer, error) {
	allSteps := bundle.Recording.AllSteps()
	if len(allSteps) == 0 {
		return nil, fmt.Errorf("bundle has no steps")
	}

	// Initialize the world at the first step
	rootHex := allSteps[0].Root
	hBytes, err := hex.DecodeString(rootHex)
	if err != nil {
		return nil, fmt.Errorf("invalid root hash in bundle: %w", err)
	}

	var h vfs.Hash
	copy(h[:], hBytes)
	world := vfs.Fork(store, vfs.Snapshot{Root: h})

	return &RunReplayer{
		bundle:   bundle,
		replayer: journal.NewReplayer(bundle.Recording.Effects),
		world:    world,
		allSteps: allSteps,
		stepIdx:  1, // We start checking from step 1 (step 0 is the init state)
	}, nil
}

// Replayer returns the underlying effect replayer.
func (r *RunReplayer) Replayer() *journal.Replayer {
	return r.replayer
}

// World returns the current active world being replayed.
func (r *RunReplayer) World() *vfs.World {
	return r.world
}

// Step verifies the current world against the next expected step in the bundle.
func (r *RunReplayer) Step(name string) (Check, error) {
	return r.StepWithParent(name, "", nil)
}

// StepWithActor verifies the current world against the next expected step in the bundle,
// validating the actor if attributed.
func (r *RunReplayer) StepWithActor(name, actor string) (Check, error) {
	return r.StepWithParent(name, actor, nil)
}

// StepWithParent verifies both top-level and nested step world hashes, actor,
// and parent step ID during replay (FR-3.10).
func (r *RunReplayer) StepWithParent(name, actor string, parentID *int) (Check, error) {
	if r.stepIdx >= len(r.allSteps) {
		return Check{}, &journal.DivergenceError{
			Class:   journal.ClassExhausted,
			Message: "agent executed more steps than recorded",
			Step:    r.stepIdx,
		}
	}

	expectedStep := r.allSteps[r.stepIdx]
	if expectedStep.Name != name {
		return Check{}, &journal.DivergenceError{
			Class:   journal.ClassStepMismatch,
			Message: fmt.Sprintf("expected %q, got %q", expectedStep.Name, name),
			Step:    r.stepIdx,
		}
	}

	if expectedStep.Actor != "" && actor != "" && expectedStep.Actor != actor {
		return Check{}, &journal.DivergenceError{
			Class:   journal.ClassStepMismatch,
			Message: fmt.Sprintf("step %q expected actor %q, got %q", name, expectedStep.Actor, actor),
			Step:    r.stepIdx,
		}
	}

	if expectedStep.ParentID != nil && parentID != nil && *expectedStep.ParentID != *parentID {
		return Check{}, &journal.DivergenceError{
			Class:   journal.ClassStepMismatch,
			Message: fmt.Sprintf("step %q expected parent_id %d, got %d", name, *expectedStep.ParentID, *parentID),
			Step:    r.stepIdx,
		}
	}

	actualSnap := r.world.Snapshot()

	hBytes, _ := hex.DecodeString(expectedStep.Root)
	var expectedHash vfs.Hash
	copy(expectedHash[:], hBytes)

	chk := Check{
		N:        expectedStep.N,
		Name:     expectedStep.Name,
		Expected: vfs.Snapshot{Root: expectedHash},
		Actual:   actualSnap,
		Match:    expectedHash == actualSnap.Root,
	}

	r.stepIdx++
	r.replayer.SetStep(r.stepIdx)
	if !chk.Match {
		return chk, &journal.DivergenceError{
			Class:   journal.ClassWorldMismatch,
			Message: fmt.Sprintf("world hash mismatch: expected %x, got %x", expectedHash[:], actualSnap.Root[:]),
			Step:    chk.N,
		}
	}
	return chk, nil
}
