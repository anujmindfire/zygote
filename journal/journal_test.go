package journal_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mindfire/zygote/journal"
)

func TestRecord(t *testing.T) {
	r := journal.NewRecorder()

	// 1. Record successful effect
	val, err := journal.ModelRecord(r, "prompt1", func() (string, error) {
		return "hello world", nil
	})
	if err != nil || val != "hello world" {
		t.Fatalf("ModelRecord failed: %v", err)
	}

	// 2. Record failed effect (should NOT be logged)
	_, err = journal.ModelRecord(r, "prompt2", func() (string, error) {
		return "", errors.New("api rate limit")
	})
	if err == nil {
		t.Fatalf("Expected error")
	}

	entries := r.Entries()
	if len(entries) != 1 {
		t.Fatalf("Expected exactly 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Seq != 0 || e.Op != "model" || e.Key != "prompt1" || string(e.Value) != "hello world" {
		t.Fatalf("Unexpected entry state: %+v", e)
	}
}

func TestReplaySuccess(t *testing.T) {
	entries := []journal.Entry{
		{Seq: 0, Op: "model", Key: "prompt1", Value: []byte("response 1")},
		{Seq: 1, Op: "tool:bash", Key: "req1", Value: []byte("ls -la")},
	}

	replayer := journal.NewReplayer(entries)

	val, err := journal.ModelReplay(replayer, "prompt1")
	if err != nil || val != "response 1" {
		t.Fatalf("Expected response 1, got %v (err: %v)", val, err)
	}

	toolVal, err := journal.ToolReplay(replayer, "bash", "req1")
	if err != nil || string(toolVal) != "ls -la" {
		t.Fatalf("Expected ls -la, got %v (err: %v)", string(toolVal), err)
	}
}

func TestDivergenceMismatch(t *testing.T) {
	entries := []journal.Entry{
		{Seq: 0, Op: "model", Key: "prompt1", Value: []byte("response 1")},
	}

	replayer := journal.NewReplayer(entries)
	replayer.SetStep(2)

	// Agent asks for prompt2 instead of prompt1
	_, err := journal.ModelReplay(replayer, "prompt2")
	if err == nil {
		t.Fatalf("Expected divergence error")
	}

	var divErr *journal.DivergenceError
	if !errors.As(err, &divErr) {
		t.Fatalf("Expected DivergenceError type, got %T", err)
	}

	if divErr.Class != journal.ClassEffectMismatch {
		t.Fatalf("Expected ClassEffectMismatch, got %v", divErr.Class)
	}
	if divErr.Step != 2 {
		t.Fatalf("Expected Step 2, got %d", divErr.Step)
	}
}

func TestDivergenceExhausted(t *testing.T) {
	replayer := journal.NewReplayer([]journal.Entry{})

	_, err := journal.ModelReplay(replayer, "prompt1")
	if err == nil {
		t.Fatalf("Expected divergence error")
	}

	var divErr *journal.DivergenceError
	if !errors.As(err, &divErr) {
		t.Fatalf("Expected DivergenceError type, got %T", err)
	}

	if divErr.Class != journal.ClassExhausted {
		t.Fatalf("Expected ClassExhausted, got %v", divErr.Class)
	}
}

func TestClockWrapper(t *testing.T) {
	r := journal.NewRecorder()
	now, err := journal.ClockRecord(r, "start")
	if err != nil {
		t.Fatalf("ClockRecord failed: %v", err)
	}

	replayer := journal.NewReplayer(r.Entries())
	replayedTime, err := journal.ClockReplay(replayer, "start")
	if err != nil {
		t.Fatalf("ClockReplay failed: %v", err)
	}

	// Must match exactly, including nanoseconds
	if !now.Equal(replayedTime) {
		t.Fatalf("Time mismatch: %v != %v", now, replayedTime)
	}
}

func TestRecordWithActor_MultiActorStreams(t *testing.T) {
	r := journal.NewRecorder()

	// Record interleaved effects from two actors
	_, err := r.RecordWithActor("actor-1", "model", "q1", func() ([]byte, error) {
		return []byte("ans-1"), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.RecordWithActor("actor-2", "tool:search", "s1", func() ([]byte, error) {
		return []byte("results-1"), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.RecordWithActor("actor-1", "model", "q2", func() ([]byte, error) {
		return []byte("ans-2"), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	entries := r.Entries()
	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}

	// Verify global sequence and per-actor sequence numbers
	if entries[0].Actor != "actor-1" || entries[0].Seq != 0 || entries[0].ActorSeq != 0 {
		t.Fatalf("Unexpected entry 0: %+v", entries[0])
	}
	if entries[1].Actor != "actor-2" || entries[1].Seq != 1 || entries[1].ActorSeq != 0 {
		t.Fatalf("Unexpected entry 1: %+v", entries[1])
	}
	if entries[2].Actor != "actor-1" || entries[2].Seq != 2 || entries[2].ActorSeq != 1 {
		t.Fatalf("Unexpected entry 2: %+v", entries[2])
	}

	// Check per-actor streams
	a1Stream := r.ActorEntries("actor-1")
	if len(a1Stream) != 2 {
		t.Fatalf("Expected 2 entries for actor-1, got %d", len(a1Stream))
	}
	if a1Stream[0].Key != "q1" || a1Stream[1].Key != "q2" {
		t.Fatalf("Unexpected stream for actor-1: %+v", a1Stream)
	}

	a2Stream := r.ActorEntries("actor-2")
	if len(a2Stream) != 1 {
		t.Fatalf("Expected 1 entry for actor-2, got %d", len(a2Stream))
	}
	if a2Stream[0].Key != "s1" {
		t.Fatalf("Unexpected stream for actor-2: %+v", a2Stream)
	}

	// Check Schedule and Actors
	sched := r.Schedule()
	if len(sched) != 3 || sched[0] != "actor-1" || sched[1] != "actor-2" || sched[2] != "actor-1" {
		t.Fatalf("Unexpected schedule: %v", sched)
	}

	actors := r.Actors()
	if len(actors) != 2 || actors[0] != "actor-1" || actors[1] != "actor-2" {
		t.Fatalf("Unexpected actors list: %v", actors)
	}
}

func TestReplay_DeterministicInterleavedScheduler(t *testing.T) {
	// Recorded interleaving schedule: [actor-A (turn 0), actor-B (turn 1), actor-A (turn 2)]
	entries := []journal.Entry{
		{Seq: 0, Actor: "actor-A", ActorSeq: 0, Op: "model", Key: "q1", Value: []byte("val-A1")},
		{Seq: 1, Actor: "actor-B", ActorSeq: 0, Op: "tool", Key: "t1", Value: []byte("val-B1")},
		{Seq: 2, Actor: "actor-A", ActorSeq: 1, Op: "model", Key: "q2", Value: []byte("val-A2")},
	}

	replayer := journal.NewReplayer(entries)

	// Simulate concurrent execution where Actor B arrives FIRST, before Actor A.
	// In the sequential replayer without interleaving scheduling, Actor B would diverge.
	// In the deterministic scheduler, Actor B waits for its turn in the schedule.
	errCh := make(chan error, 2)
	bReady := make(chan struct{})

	go func() {
		// Actor B runs
		close(bReady)
		val, err := replayer.ReplayWithActor("actor-B", "tool", "t1")
		if err != nil {
			errCh <- err
			return
		}
		if string(val) != "val-B1" {
			errCh <- errors.New("actor-B got wrong value")
			return
		}
		errCh <- nil
	}()

	<-bReady
	// Give Actor B goroutine a moment to attempt Replay first and wait for its turn
	time.Sleep(10 * time.Millisecond)

	go func() {
		// Actor A runs
		val1, err := replayer.ReplayWithActor("actor-A", "model", "q1")
		if err != nil {
			errCh <- err
			return
		}
		if string(val1) != "val-A1" {
			errCh <- errors.New("actor-A step 1 got wrong value")
			return
		}

		val2, err := replayer.ReplayWithActor("actor-A", "model", "q2")
		if err != nil {
			errCh <- err
			return
		}
		if string(val2) != "val-A2" {
			errCh <- errors.New("actor-A step 2 got wrong value")
			return
		}
		errCh <- nil
	}()

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("Goroutine error: %v", err)
		}
	}
}

func TestReplay_MultiActorEffectMismatch(t *testing.T) {
	entries := []journal.Entry{
		{Seq: 0, Actor: "agent-1", ActorSeq: 0, Op: "model", Key: "prompt-1", Value: []byte("hello")},
	}

	replayer := journal.NewReplayer(entries)

	_, err := replayer.ReplayWithActor("agent-1", "model", "prompt-WRONG")
	if err == nil {
		t.Fatal("Expected divergence error")
	}

	var divErr *journal.DivergenceError
	if !errors.As(err, &divErr) {
		t.Fatalf("Expected DivergenceError, got %T", err)
	}
	if divErr.Class != journal.ClassEffectMismatch {
		t.Fatalf("Expected ClassEffectMismatch, got %v", divErr.Class)
	}
}

func TestReplay_MultiActorExhausted(t *testing.T) {
	entries := []journal.Entry{
		{Seq: 0, Actor: "agent-1", ActorSeq: 0, Op: "model", Key: "prompt-1", Value: []byte("hello")},
	}

	replayer := journal.NewReplayer(entries)

	// First call succeeds
	_, err := replayer.ReplayWithActor("agent-1", "model", "prompt-1")
	if err != nil {
		t.Fatal(err)
	}

	// Second call exhausts actor stream
	_, err = replayer.ReplayWithActor("agent-1", "model", "prompt-2")
	if err == nil {
		t.Fatal("Expected divergence error")
	}

	var divErr *journal.DivergenceError
	if !errors.As(err, &divErr) {
		t.Fatalf("Expected DivergenceError, got %T", err)
	}
	if divErr.Class != journal.ClassExhausted {
		t.Fatalf("Expected ClassExhausted, got %v", divErr.Class)
	}
}

func TestReplay_SchedulerRelaxed(t *testing.T) {
	entries := []journal.Entry{
		{Seq: 0, Actor: "agent-A", ActorSeq: 0, Op: "model", Key: "qA", Value: []byte("resA")},
		{Seq: 1, Actor: "agent-B", ActorSeq: 0, Op: "tool", Key: "qB", Value: []byte("resB")},
	}

	replayer := journal.NewReplayer(entries)
	replayer.SetSchedulerMode(journal.SchedulerRelaxed)

	// Under SchedulerRelaxed, agent-B can replay its effect first without waiting for agent-A
	valB, err := replayer.ReplayWithActor("agent-B", "tool", "qB")
	if err != nil {
		t.Fatal(err)
	}
	if string(valB) != "resB" {
		t.Fatalf("Expected resB, got %s", string(valB))
	}

	valA, err := replayer.ReplayWithActor("agent-A", "model", "qA")
	if err != nil {
		t.Fatal(err)
	}
	if string(valA) != "resA" {
		t.Fatalf("Expected resA, got %s", string(valA))
	}
}

func TestReplay_InterleavedTimeout(t *testing.T) {
	entries := []journal.Entry{
		{Seq: 0, Actor: "agent-A", ActorSeq: 0, Op: "model", Key: "qA", Value: []byte("resA")},
		{Seq: 1, Actor: "agent-B", ActorSeq: 0, Op: "tool", Key: "qB", Value: []byte("resB")},
	}

	replayer := journal.NewReplayer(entries)
	replayer.SetTimeout(50 * time.Millisecond) // Short timeout for test

	// Agent B arrives, but Agent A never runs. Agent B should timeout and report divergence.
	_, err := replayer.ReplayWithActor("agent-B", "tool", "qB")
	if err == nil {
		t.Fatal("Expected timeout divergence error")
	}

	var divErr *journal.DivergenceError
	if !errors.As(err, &divErr) {
		t.Fatalf("Expected DivergenceError, got %T", err)
	}
	if divErr.Class != journal.ClassEffectMismatch {
		t.Fatalf("Expected ClassEffectMismatch, got %v", divErr.Class)
	}
}
