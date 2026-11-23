// Package journal provides effects.
package journal

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// SchedulerMode defines how concurrent effects are coordinated during replay.
type SchedulerMode int

const (
	// SchedulerStrict coordinates concurrent actors to follow the exact recorded
	// interleaving schedule. Actors wait for their turn according to the recorded sequence.
	SchedulerStrict SchedulerMode = iota

	// SchedulerRelaxed substitutes effects from each actor's independent stream
	// without coordinating interleaving between actors.
	SchedulerRelaxed
)

// normalizeActor returns a standard canonical actor name. Empty strings default to "default".
func normalizeActor(actor string) string {
	if actor == "" {
		return "default"
	}
	return actor
}

// Recorder maintains an append-only log of non-deterministic effects.
type Recorder struct {
	mu        sync.Mutex
	entries   []Entry
	seq       int
	actorSeqs map[string]int
}

// NewRecorder initializes a new empty recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		entries:   make([]Entry, 0),
		seq:       0,
		actorSeqs: make(map[string]int),
	}
}

// Record invokes the provided function and, if successful, logs the result.
// Fulfills FR-2.5: A failed effect aborts the recording and is not logged.
func (r *Recorder) Record(op, key string, fn func() ([]byte, error)) ([]byte, error) {
	return r.RecordWithActor("", op, key, fn)
}

// RecordWithActor invokes the function and logs the result under a specific actor/thread identifier.
// Fulfills FR-8.6 and OD-4 for multi-actor interleaved effect tracking.
func (r *Recorder) RecordWithActor(actor, op, key string, fn func() ([]byte, error)) ([]byte, error) {
	val, err := fn()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	norm := normalizeActor(actor)
	actorSeq := r.actorSeqs[norm]
	r.actorSeqs[norm]++

	entry := Entry{
		Seq:      r.seq,
		Actor:    actor,
		ActorSeq: actorSeq,
		Op:       op,
		Key:      key,
		Value:    val,
	}
	r.entries = append(r.entries, entry)
	r.seq++

	return val, nil
}

// Entries returns a copy of the recorded effect log.
func (r *Recorder) Entries() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// ActorEntries returns a copy of the recorded effect log for a specific actor.
func (r *Recorder) ActorEntries(actor string) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()

	norm := normalizeActor(actor)
	var out []Entry
	for _, e := range r.entries {
		if normalizeActor(e.Actor) == norm {
			out = append(out, e)
		}
	}
	return out
}

// Schedule returns the recorded interleaving sequence of actors.
func (r *Recorder) Schedule() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, len(r.entries))
	for i, e := range r.entries {
		out[i] = normalizeActor(e.Actor)
	}
	return out
}

// Actors returns all distinct actors observed during recording.
func (r *Recorder) Actors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[string]bool)
	var out []string
	for _, e := range r.entries {
		norm := normalizeActor(e.Actor)
		if !seen[norm] {
			seen[norm] = true
			out = append(out, norm)
		}
	}
	return out
}

// Replayer strictly substitutes recorded effects for live execution.
// Supports deterministic replay scheduling across concurrent actors (FR-8.6, OD-4).
type Replayer struct {
	mu            sync.Mutex
	entries       []Entry
	actorStreams  map[string][]Entry
	actorPos      map[string]int
	schedule      []string
	globalPos     int
	step          int
	mode          SchedulerMode
	timeout       time.Duration
	wakeCh        chan struct{}
	closed        bool
	divergenceErr error
}

// NewReplayer initializes a replayer with a pre-recorded log.
func NewReplayer(entries []Entry) *Replayer {
	r := &Replayer{
		entries:      entries,
		actorStreams: make(map[string][]Entry),
		actorPos:     make(map[string]int),
		schedule:     make([]string, len(entries)),
		wakeCh:       make(chan struct{}),
		mode:         SchedulerStrict,
		timeout:      5 * time.Second,
	}

	for i, e := range entries {
		norm := normalizeActor(e.Actor)
		r.actorStreams[norm] = append(r.actorStreams[norm], e)
		r.schedule[i] = norm
	}

	return r
}

// SetStep updates the current step number for divergence reporting.
func (r *Replayer) SetStep(step int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.step = step
}

// SetSchedulerMode sets the replay scheduler mode (Strict or Relaxed).
func (r *Replayer) SetSchedulerMode(mode SchedulerMode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode = mode
}

// SetTimeout sets the maximum duration to wait for an actor's turn in the interleaving schedule.
func (r *Replayer) SetTimeout(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timeout = d
}

// Close aborts waiting actors and closes the replayer.
func (r *Replayer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.broadcast()
}

func (r *Replayer) broadcast() {
	close(r.wakeCh)
	r.wakeCh = make(chan struct{})
}

// Replay returns the recorded value for the requested operation for the default actor.
// Fulfills FR-2.2: The underlying effect function is never invoked.
// Fulfills FR-2.3 and FR-2.4: Mismatches and exhaustion return DivergenceError.
func (r *Replayer) Replay(op, key string) ([]byte, error) {
	return r.ReplayWithActor("", op, key)
}

// ReplayWithActor returns the recorded value for the requested operation for a specific actor.
// In SchedulerStrict mode, coordinates with the deterministic interleaving schedule (FR-8.6, OD-4).
func (r *Replayer) ReplayWithActor(actor, op, key string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	norm := normalizeActor(actor)

	if r.closed {
		return nil, errors.New("replayer is closed")
	}
	if r.divergenceErr != nil {
		return nil, r.divergenceErr
	}

	stream := r.actorStreams[norm]
	pos := r.actorPos[norm]

	if pos >= len(stream) {
		return nil, &DivergenceError{
			Class:   ClassExhausted,
			Message: "run wants more effects than recorded",
			Step:    r.step,
		}
	}

	if r.mode == SchedulerStrict {
		// Wait until it is this actor's turn according to the recorded interleaving schedule.
		start := time.Now()
		for r.globalPos < len(r.schedule) && r.schedule[r.globalPos] != norm {
			if r.closed {
				return nil, errors.New("replayer is closed")
			}
			if r.divergenceErr != nil {
				return nil, r.divergenceErr
			}

			wakeCh := r.wakeCh
			timeout := r.timeout
			r.mu.Unlock()

			if timeout > 0 {
				elapsed := time.Since(start)
				remaining := timeout - elapsed
				if remaining <= 0 {
					r.mu.Lock()
					err := &DivergenceError{
						Class:   ClassEffectMismatch,
						Message: fmt.Sprintf("timed out waiting for actor %q turn in interleaving schedule (turn %d is %q)", actor, r.globalPos, r.schedule[r.globalPos]),
						Step:    r.step,
					}
					r.divergenceErr = err
					r.broadcast()
					return nil, err
				}

				select {
				case <-wakeCh:
					r.mu.Lock()
				case <-time.After(remaining):
					r.mu.Lock()
					if r.globalPos < len(r.schedule) && r.schedule[r.globalPos] != norm {
						err := &DivergenceError{
							Class:   ClassEffectMismatch,
							Message: fmt.Sprintf("timed out waiting for actor %q turn in interleaving schedule (turn %d is %q)", actor, r.globalPos, r.schedule[r.globalPos]),
							Step:    r.step,
						}
						r.divergenceErr = err
						r.broadcast()
						return nil, err
					}
				}
			} else {
				<-wakeCh
				r.mu.Lock()
			}
		}

		if r.closed {
			return nil, errors.New("replayer is closed")
		}
		if r.divergenceErr != nil {
			return nil, r.divergenceErr
		}

		if r.globalPos >= len(r.schedule) {
			return nil, &DivergenceError{
				Class:   ClassExhausted,
				Message: "run wants more effects than recorded",
				Step:    r.step,
			}
		}
	}

	// Verify the effect against the actor's stream
	entry := stream[pos]
	if entry.Op != op || entry.Key != key {
		err := &DivergenceError{
			Class:   ClassEffectMismatch,
			Message: "different op/key requested than recorded",
			Step:    r.step,
		}
		r.divergenceErr = err
		r.broadcast()
		return nil, err
	}

	r.actorPos[norm]++
	if r.mode == SchedulerStrict {
		r.globalPos++
		r.broadcast()
	}

	return entry.Value, nil
}

// Schedule returns the recorded interleaving sequence of actors.
func (r *Replayer) Schedule() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, len(r.schedule))
	copy(out, r.schedule)
	return out
}

// Actors returns all distinct actors in the replayer's log.
func (r *Replayer) Actors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[string]bool)
	var out []string
	for _, a := range r.schedule {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// ActorEntries returns a copy of the recorded effect log for a specific actor.
func (r *Replayer) ActorEntries(actor string) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()

	norm := normalizeActor(actor)
	stream := r.actorStreams[norm]
	out := make([]Entry, len(stream))
	copy(out, stream)
	return out
}
