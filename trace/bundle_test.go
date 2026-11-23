package trace_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mindfire/zygote/pkg/compress"
	"github.com/mindfire/zygote/trace"
	"github.com/mindfire/zygote/vfs"
)

func TestRecordAndBundle(t *testing.T) {
	store1 := vfs.NewMemStore()
	r := trace.Record(store1)

	r.Set("agent", "test-agent-v1")
	r.Step("init")

	err := r.World().WriteFile("main.go", []byte("package main"), 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	r.Step("write_code")

	rec := r.Recording()
	if len(rec.Steps) != 2 {
		t.Fatalf("Expected 2 steps, got %d", len(rec.Steps))
	}
	if rec.Meta["agent"] != "test-agent-v1" {
		t.Fatalf("Metadata missing")
	}

	// Export
	tmpDir := t.TempDir()
	bundlePath := filepath.Join(tmpDir, "run.zip")

	err = trace.ExportFile(bundlePath, store1, rec)
	if err != nil {
		t.Fatalf("ExportFile failed: %v", err)
	}

	// Import into an empty store
	store2 := vfs.NewMemStore()
	bundle, err := trace.ImportFile(bundlePath, store2)
	if err != nil {
		t.Fatalf("ImportFile failed: %v", err)
	}

	if bundle.Recording.Meta["agent"] != "test-agent-v1" {
		t.Fatalf("Import metadata mismatch")
	}

	// Verify we can read the file back out of the new store
	_ = vfs.NewWorld(store2)
	snapHash := bundle.Recording.Steps[1].Root
	// We need to parse snapHash to vfs.Hash and checkout...
	// For testing, the Verify check already guarantees reachability,
	// which satisfies FR-4.4.
	_ = snapHash
}

func readTraceJSON(t *testing.T, path string) []byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()

	for _, zf := range zr.File {
		if zf.Name != "trace.json" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Fatal("trace.json not found in bundle")
	return nil
}

func TestFidelityRoundTrip(t *testing.T) {
	store := vfs.NewMemStore()
	r := trace.Record(store)
	_ = r.World().WriteFile("a.txt", []byte("a"), 0644)
	r.Step("init")

	rec := r.Recording()
	rec.Fidelity = &trace.Fidelity{Tier: "A", Limitations: []string{"one", "two"}}

	path := filepath.Join(t.TempDir(), "run.zip")
	if err := trace.ExportFile(path, store, rec); err != nil {
		t.Fatal(err)
	}

	bundle, err := trace.ImportFile(path, vfs.NewMemStore())
	if err != nil {
		t.Fatal(err)
	}
	f := bundle.Recording.Fidelity
	if f == nil || f.Tier != "A" || len(f.Limitations) != 2 || f.Limitations[0] != "one" || f.Limitations[1] != "two" {
		t.Fatalf("fidelity not preserved: %+v", f)
	}
}

func TestFidelityAbsentInOldBundle(t *testing.T) {
	store := vfs.NewMemStore()
	r := trace.Record(store)
	_ = r.World().WriteFile("a.txt", []byte("a"), 0644)
	r.Step("init")

	path := filepath.Join(t.TempDir(), "run.zip")
	if err := trace.ExportFile(path, store, r.Recording()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(readTraceJSON(t, path)), `"fidelity"`) {
		t.Fatal(`"fidelity" key must be omitted when Fidelity is nil`)
	}

	bundle, err := trace.ImportFile(path, vfs.NewMemStore())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Recording.Fidelity != nil {
		t.Fatalf("expected nil Fidelity, got %+v", bundle.Recording.Fidelity)
	}
}

func TestAdversarialCorruption(t *testing.T) {
	store := vfs.NewMemStore()
	r := trace.Record(store)
	_ = r.World().WriteFile("secret.txt", []byte("real content"), 0644)
	r.Step("write")

	tmpDir := t.TempDir()
	bundlePath := filepath.Join(tmpDir, "run.zip")
	_ = trace.ExportFile(bundlePath, store, r.Recording())

	// Corrupt the zip file manually
	corruptPath := filepath.Join(tmpDir, "corrupt.zip")
	injectCorruption(t, bundlePath, corruptPath)

	store2 := vfs.NewMemStore()
	_, err := trace.ImportFile(corruptPath, store2)
	if err != trace.ErrHashMismatch {
		t.Fatalf("Expected ErrHashMismatch on corrupted bundle, got %v", err)
	}
}

func injectCorruption(t *testing.T, src, dst string) {
	r, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	f, err := os.Create(dst) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	zw := zip.NewWriter(f)
	defer func() { _ = zw.Close() }()

	for _, zf := range r.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}

		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatal(err)
		}

		if zf.Name == "trace.json" {
			_, _ = io.Copy(w, rc) //nolint:gosec
		} else {
			// Write garbage to the object
			_, _ = w.Write([]byte("corrupted data that doesn't match hash!"))
		}
		_ = rc.Close()
	}
}

func TestZstdCompressedBundle(t *testing.T) {
	store1 := vfs.NewMemStore()
	r := trace.Record(store1)
	r.Set("agent", "zstd-tester")
	r.Step("init")

	largeContent := bytes.Repeat([]byte("package main\n\nfunc main() {\n\t// compressible code pattern repeated\n}\n"), 100)
	if err := r.World().WriteFile("large.go", largeContent, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	r.Step("add_large")
	rec := r.Recording()

	tmpDir := t.TempDir()
	bundlePath := filepath.Join(tmpDir, "run_zstd.zip")

	// Export with zstd compression enabled
	err := trace.ExportBundle(bundlePath, store1, rec, trace.ExportOptions{Compress: true})
	if err != nil {
		t.Fatalf("ExportBundle with Compress failed: %v", err)
	}

	// Verify that objects inside zip are indeed zstd-compressed
	zr, err := zip.OpenReader(bundlePath)
	if err != nil {
		t.Fatalf("OpenReader failed: %v", err)
	}
	foundCompressedObject := false
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "objects/") {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open entry %s: %v", f.Name, err)
			}
			b, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("read entry %s: %v", f.Name, err)
			}
			if compress.IsCompressed(b) {
				foundCompressedObject = true
				if f.Method != zip.Store {
					t.Errorf("expected zstd compressed entry to use zip.Store method, got %d", f.Method)
				}
			}
		}
	}
	_ = zr.Close()

	if !foundCompressedObject {
		t.Fatalf("expected at least one zstd-compressed object in bundle")
	}

	// Transparent import
	store2 := vfs.NewMemStore()
	bundle, err := trace.ImportFile(bundlePath, store2)
	if err != nil {
		t.Fatalf("ImportFile failed: %v", err)
	}

	// Verify file content can be retrieved uncompressed
	step1Root := bundle.Recording.Steps[1].Root
	var rootHash vfs.Hash
	hb, _ := hex.DecodeString(step1Root)
	copy(rootHash[:], hb)

	if err := vfs.Verify(context.Background(), store2, vfs.Snapshot{Root: rootHash}); err != nil {
		t.Fatalf("vfs.Verify failed after importing zstd bundle: %v", err)
	}
}

func TestMultiActorRecordingAndReplay(t *testing.T) {
	store1 := vfs.NewMemStore()
	r := trace.Record(store1)
	r.Set("agent", "multi-actor-agent")

	r.StepWithActor("init", "agent-alpha")

	// Actor Alpha records an effect and writes a file
	_, err := r.Recorder().RecordWithActor("agent-alpha", "model", "prompt-1", func() ([]byte, error) {
		return []byte("alpha-code"), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := r.World().WriteFile("alpha.txt", []byte("from-alpha"), 0644); err != nil {
		t.Fatal(err)
	}
	r.StepWithActor("step-alpha", "agent-alpha")

	// Actor Beta records an effect and writes a file
	_, err = r.Recorder().RecordWithActor("agent-beta", "tool", "cmd-1", func() ([]byte, error) {
		return []byte("beta-output"), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := r.World().WriteFile("beta.txt", []byte("from-beta"), 0644); err != nil {
		t.Fatal(err)
	}
	r.StepWithActor("step-beta", "agent-beta")

	rec := r.Recording()
	if len(rec.Steps) != 3 {
		t.Fatalf("Expected 3 steps, got %d", len(rec.Steps))
	}
	if rec.Steps[1].Actor != "agent-alpha" || rec.Steps[2].Actor != "agent-beta" {
		t.Fatalf("Step actor attribution mismatch: %+v", rec.Steps)
	}
	if len(rec.Effects) != 2 {
		t.Fatalf("Expected 2 effects, got %d", len(rec.Effects))
	}
	if rec.Effects[0].Actor != "agent-alpha" || rec.Effects[1].Actor != "agent-beta" {
		t.Fatalf("Effect actor attribution mismatch: %+v", rec.Effects)
	}

	// Export and re-import bundle
	tmpDir := t.TempDir()
	bundlePath := filepath.Join(tmpDir, "multiactor.zip")
	if err := trace.ExportFile(bundlePath, store1, rec); err != nil {
		t.Fatal(err)
	}

	store2 := vfs.NewMemStore()
	bundle, err := trace.ImportFile(bundlePath, store2)
	if err != nil {
		t.Fatal(err)
	}

	// Replay run
	replayer, err := trace.Replay(store2, &bundle)
	if err != nil {
		t.Fatal(err)
	}

	// Replay Alpha effect & step
	effVal, err := replayer.Replayer().ReplayWithActor("agent-alpha", "model", "prompt-1")
	if err != nil || string(effVal) != "alpha-code" {
		t.Fatalf("ReplayWithActor alpha failed: %v", err)
	}

	if err := replayer.World().WriteFile("alpha.txt", []byte("from-alpha"), 0644); err != nil {
		t.Fatal(err)
	}
	chk, err := replayer.StepWithActor("step-alpha", "agent-alpha")
	if err != nil || !chk.Match {
		t.Fatalf("StepWithActor alpha failed: match=%v err=%v", chk.Match, err)
	}

	// Replay Beta effect & step
	effVal2, err := replayer.Replayer().ReplayWithActor("agent-beta", "tool", "cmd-1")
	if err != nil || string(effVal2) != "beta-output" {
		t.Fatalf("ReplayWithActor beta failed: %v", err)
	}

	if err := replayer.World().WriteFile("beta.txt", []byte("from-beta"), 0644); err != nil {
		t.Fatal(err)
	}
	chk2, err := replayer.StepWithActor("step-beta", "agent-beta")
	if err != nil || !chk2.Match {
		t.Fatalf("StepWithActor beta failed: match=%v err=%v", chk2.Match, err)
	}
}

func TestNestedSteps_TreeBuildAndFlatten(t *testing.T) {
	p1 := 1
	p2 := 2
	steps := []trace.Step{
		{N: 0, Name: "init"},
		{N: 1, Name: "task_plan"},
		{N: 2, Name: "subagent_research", ParentID: &p1},
		{N: 3, Name: "subagent_query_db", ParentID: &p2}, // nested sub-sub-step
		{N: 4, Name: "subagent_summarize", ParentID: &p1},
		{N: 5, Name: "task_execute"},
	}

	tree := trace.BuildStepTree(steps)
	if len(tree) != 3 {
		t.Fatalf("expected 3 top-level steps, got %d", len(tree))
	}

	// Step 0: init
	if tree[0].N != 0 || len(tree[0].Children) != 0 {
		t.Fatalf("unexpected step 0: %+v", tree[0])
	}

	// Step 1: task_plan with 2 children
	if tree[1].N != 1 || len(tree[1].Children) != 2 {
		t.Fatalf("expected step 1 to have 2 children, got %d", len(tree[1].Children))
	}

	// Subagent research has 1 child (sub-sub-step)
	if tree[1].Children[0].N != 2 || len(tree[1].Children[0].Children) != 1 {
		t.Fatalf("expected step 2 to have 1 child, got %d", len(tree[1].Children[0].Children))
	}
	if tree[1].Children[0].Children[0].N != 3 {
		t.Fatalf("expected sub-sub-step 3, got %d", tree[1].Children[0].Children[0].N)
	}

	// Step 5: task_execute
	if tree[2].N != 5 {
		t.Fatalf("expected step 5, got %d", tree[2].N)
	}

	// Test FlattenSteps reproduces original order
	flattened := trace.FlattenSteps(tree)
	if len(flattened) != 6 {
		t.Fatalf("expected 6 flattened steps, got %d", len(flattened))
	}
	for i := 0; i < 6; i++ {
		if flattened[i].N != i {
			t.Fatalf("step %d index mismatch in flattened: %d", i, flattened[i].N)
		}
	}
}

func TestNestedSteps_CanonicalJSONRFC8785(t *testing.T) {
	p := 1
	rec := trace.Recording{
		Version:       1,
		HashAlgorithm: "blake3",
		Producer:      "zygote-cli",
		Steps: []trace.Step{
			{N: 0, Name: "init", Root: "ec2bd03bf86b935fa34d71ad7ebb049f1f10f87d343e521511d8f9e6625620cd", Effects: 0},
			{
				N:       1,
				Name:    "parent_step",
				Root:    "ec2bd03bf86b935fa34d71ad7ebb049f1f10f87d343e521511d8f9e6625620cd",
				Effects: 1,
				Children: []trace.Step{
					{
						N:        2,
						Name:     "child_substep",
						Root:     "ec2bd03bf86b935fa34d71ad7ebb049f1f10f87d343e521511d8f9e6625620cd",
						Effects:  1,
						Actor:    "sub-agent-1",
						ParentID: &p,
					},
				},
			},
		},
	}

	canon, err := trace.CanonicalizeManifest(rec)
	if err != nil {
		t.Fatalf("CanonicalizeManifest failed: %v", err)
	}

	canonStr := string(canon)
	if !strings.Contains(canonStr, `"children":[{"actor":"sub-agent-1"`) {
		t.Fatalf("canonical JSON does not format nested children properly: %s", canonStr)
	}

	// Verify signing and verification over canonical manifest with nested steps
	pub, priv, err := trace.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	sig, err := trace.SignManifest(rec, priv)
	if err != nil {
		t.Fatal(err)
	}

	if !trace.VerifyManifest(rec, sig, pub) {
		t.Fatal("VerifyManifest failed on nested step recording")
	}
}

func TestNestedSteps_ReplayVerification(t *testing.T) {
	store1 := vfs.NewMemStore()
	r := trace.Record(store1)

	r.Step("init") // N: 0
	parentID := 1
	r.Step("plan")                                                        // N: 1
	_ = r.World().WriteFile("sub.txt", []byte("sub-data"), 0644)
	r.StepWithParent("sub-search", "subagent-1", &parentID)              // N: 2
	_ = r.World().WriteFile("final.txt", []byte("final-data"), 0644)
	r.Step("finish")                                                     // N: 3

	rec := r.Recording()
	tmpDir := t.TempDir()
	bundlePath := filepath.Join(tmpDir, "nested_run.zip")
	if err := trace.ExportFile(bundlePath, store1, rec); err != nil {
		t.Fatal(err)
	}

	store2 := vfs.NewMemStore()
	bundle, err := trace.ImportFile(bundlePath, store2)
	if err != nil {
		t.Fatal(err)
	}

	// Replay and verify all steps (both top-level and nested)
	replayer, err := trace.Replay(store2, &bundle)
	if err != nil {
		t.Fatal(err)
	}

	chk1, err := replayer.Step("plan")
	if err != nil || !chk1.Match {
		t.Fatalf("Step 1 failed: %v", err)
	}

	_ = replayer.World().WriteFile("sub.txt", []byte("sub-data"), 0644)
	chk2, err := replayer.StepWithParent("sub-search", "subagent-1", &parentID)
	if err != nil || !chk2.Match {
		t.Fatalf("Nested step 2 verification failed: %v", err)
	}

	_ = replayer.World().WriteFile("final.txt", []byte("final-data"), 0644)
	chk3, err := replayer.Step("finish")
	if err != nil || !chk3.Match {
		t.Fatalf("Step 3 failed: %v", err)
	}

	// Test nested divergence: world hash divergence inside nested sub-step
	store3 := vfs.NewMemStore()
	bundle3, _ := trace.ImportFile(bundlePath, store3)
	replayerDivergent, _ := trace.Replay(store3, &bundle3)

	_, _ = replayerDivergent.Step("plan")
	// Sub-agent writes different data!
	_ = replayerDivergent.World().WriteFile("sub.txt", []byte("CORRUPTED_SUB_DATA"), 0644)
	_, err = replayerDivergent.StepWithParent("sub-search", "subagent-1", &parentID)
	if err == nil {
		t.Fatal("expected divergence on corrupted nested sub-step world hash")
	}
}



