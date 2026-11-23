package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/vfs"
)

// TestLoadAndApplyRoundTrip verifies Load reads existing files into World and Apply writes mutations back.
func TestLoadAndApplyRoundTrip(t *testing.T, f AdapterFactory) {
	t.Helper()

	initialFiles := map[string][]byte{
		"file1.txt":             []byte("hello world"),
		"nested/sub/data.json": []byte(`{"key": "value"}`),
	}

	adapter, fixture := f.Create(t, initialFiles, Options{})
	defer fixture.Cleanup()

	store := vfs.NewMemStore()
	w := vfs.NewWorld(store)

	if err := adapter.Load(context.Background(), w); err != nil {
		t.Fatalf("[%s] Load failed: %v", f.Name(), err)
	}

	// Verify loaded content
	data1, err := w.ReadFile("file1.txt")
	if err != nil {
		t.Fatalf("[%s] file1.txt not found in loaded world: %v", f.Name(), err)
	}
	if !bytes.Equal(data1, initialFiles["file1.txt"]) {
		t.Fatalf("[%s] file1.txt content mismatch: got %q, want %q", f.Name(), string(data1), string(initialFiles["file1.txt"]))
	}

	data2, err := w.ReadFile("nested/sub/data.json")
	if err != nil {
		t.Fatalf("[%s] nested/sub/data.json not found in loaded world: %v", f.Name(), err)
	}
	if !bytes.Equal(data2, initialFiles["nested/sub/data.json"]) {
		t.Fatalf("[%s] nested/sub/data.json content mismatch: got %q, want %q", f.Name(), string(data2), string(initialFiles["nested/sub/data.json"]))
	}

	// Mutate world
	updatedContent := []byte("updated content in file1")
	createdContent := []byte("brand new file content")

	if err := w.WriteFile("file1.txt", updatedContent, 0644); err != nil {
		t.Fatalf("[%s] failed to update file1.txt in world: %v", f.Name(), err)
	}
	if err := w.WriteFile("created.txt", createdContent, 0644); err != nil {
		t.Fatalf("[%s] failed to create created.txt in world: %v", f.Name(), err)
	}

	// Apply back
	if err := adapter.Apply(context.Background(), w); err != nil {
		t.Fatalf("[%s] Apply failed: %v", f.Name(), err)
	}

	// Inspect fixture
	gotUpdated, err := fixture.ReadFile(t, "file1.txt")
	if err != nil {
		t.Fatalf("[%s] failed to read updated file1.txt from fixture: %v", f.Name(), err)
	}
	if !bytes.Equal(gotUpdated, updatedContent) {
		t.Fatalf("[%s] updated file1.txt mismatch in fixture: got %q, want %q", f.Name(), string(gotUpdated), string(updatedContent))
	}

	gotCreated, err := fixture.ReadFile(t, "created.txt")
	if err != nil {
		t.Fatalf("[%s] failed to read created.txt from fixture: %v", f.Name(), err)
	}
	if !bytes.Equal(gotCreated, createdContent) {
		t.Fatalf("[%s] created.txt mismatch in fixture: got %q, want %q", f.Name(), string(gotCreated), string(createdContent))
	}

	gotPreserved, err := fixture.ReadFile(t, "nested/sub/data.json")
	if err != nil {
		t.Fatalf("[%s] failed to read preserved nested/sub/data.json from fixture: %v", f.Name(), err)
	}
	if !bytes.Equal(gotPreserved, initialFiles["nested/sub/data.json"]) {
		t.Fatalf("[%s] preserved nested/sub/data.json mismatch in fixture", f.Name())
	}
}

// TestPathTraversal verifies strict rejection of directory traversal and path escapes (FR-7.5, NFR-4.1).
func TestPathTraversal(t *testing.T, f AdapterFactory) {
	t.Helper()

	escapeVectors := []string{
		"../escaped.txt",
		"../../etc/passwd",
		"sub/../../escaped.txt",
	}

	for _, escapePath := range escapeVectors {
		t.Run(escapePath, func(t *testing.T) {
			adapter, fixture := f.Create(t, nil, Options{})
			defer fixture.Cleanup()

			store := vfs.NewMemStore()
			blobHash, err := store.Put([]byte("traversal attack payload"))
			if err != nil {
				t.Fatalf("failed to put blob: %v", err)
			}

			maliciousTree := vfs.Tree{
				{
					Name: escapePath,
					Kind: vfs.KindFile,
					Mode: 0644,
					Size: int64(len("traversal attack payload")),
					Hash: blobHash,
				},
			}

			rootHash, err := store.Put(maliciousTree.Encode())
			if err != nil {
				t.Fatalf("failed to encode malicious tree: %v", err)
			}

			maliciousWorld := vfs.Fork(store, vfs.Snapshot{Root: rootHash})

			err = adapter.Apply(context.Background(), maliciousWorld)
			if err == nil {
				t.Fatalf("[%s] Apply MUST reject path traversal %q, but succeeded", f.Name(), escapePath)
			}

			// Must indicate path traversal / escape
			isEscapeErr := errors.Is(err, source.ErrPathEscape) ||
				strings.Contains(strings.ToLower(err.Error()), "escape") ||
				strings.Contains(strings.ToLower(err.Error()), "traversal") ||
				strings.Contains(strings.ToLower(err.Error()), "security")

			if !isEscapeErr {
				t.Fatalf("[%s] expected path escape error for %q, got: %v", f.Name(), escapePath, err)
			}

			// Ensure file was not created
			if fixture.FileExists(t, escapePath) {
				t.Fatalf("[%s] traversal file was written to fixture: %s", f.Name(), escapePath)
			}

			// If fixture has a root dir, verify parent dir does not have the escaped file
			if root := fixture.RootDir(); root != "" {
				parentFile := filepath.Join(filepath.Dir(root), "escaped.txt")
				if _, err := os.Stat(parentFile); err == nil {
					_ = os.Remove(parentFile)
					t.Fatalf("[%s] traversal file escaped to disk parent directory: %s", f.Name(), parentFile)
				}
			}
		})
	}
}

// TestFilePermissions verifies file permission handling and declarations.
func TestFilePermissions(t *testing.T, f AdapterFactory) {
	t.Helper()

	caps := f.Capabilities()
	initialFiles := map[string][]byte{
		"run.sh":      []byte("#!/bin/sh\necho zygote\n"),
		"config.json": []byte("{}"),
	}

	adapter, fixture := f.Create(t, initialFiles, Options{})
	defer fixture.Cleanup()

	store := vfs.NewMemStore()
	w := vfs.NewWorld(store)

	if err := adapter.Load(context.Background(), w); err != nil {
		t.Fatalf("[%s] Load failed: %v", f.Name(), err)
	}

	if caps.PreservesPermissionsOnLoad {
		info, err := fs.Stat(w, "run.sh")
		if err != nil {
			t.Fatalf("[%s] stat run.sh in world failed: %v", f.Name(), err)
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("[%s] expected executable bit preserved on Load, mode was %v", f.Name(), info.Mode())
		}
	}

	// Test Apply permissions
	if err := w.WriteFile("exec.bin", []byte("binary"), 0755); err != nil {
		t.Fatalf("[%s] write exec.bin failed: %v", f.Name(), err)
	}
	if err := adapter.Apply(context.Background(), w); err != nil {
		t.Fatalf("[%s] Apply failed: %v", f.Name(), err)
	}

	if caps.PreservesPermissionsOnApply {
		mode, err := fixture.GetFileMode(t, "exec.bin")
		if err != nil {
			t.Fatalf("[%s] GetFileMode failed: %v", f.Name(), err)
		}
		if mode&0111 == 0 {
			t.Errorf("[%s] expected executable bit preserved on Apply, mode was %v", f.Name(), mode)
		}
	} else {
		// If permissions are not preserved on Apply, verify that Fidelity declares it honestly
		limitations := strings.Join(adapter.Fidelity().Limitations, " ")
		if !strings.Contains(strings.ToLower(limitations), "permission") &&
			!strings.Contains(strings.ToLower(limitations), "mode") {
			t.Errorf("[%s] adapter does not preserve permissions on Apply but does not document it in Fidelity limitations: %v", f.Name(), adapter.Fidelity().Limitations)
		}
	}
}

// TestExclusionPatterns verifies exclusion glob patterns (FR-7.2).
func TestExclusionPatterns(t *testing.T, f AdapterFactory) {
	t.Helper()

	if !f.Capabilities().SupportsExclusionGlobs {
		t.Skip("adapter does not support exclusion globs")
	}

	opts := Options{
		ExcludeGlobs: []string{".git", "*.secret", "ignored_dir"},
	}

	initialFiles := map[string][]byte{
		"main.go":               []byte("package main"),
		".git/config":           []byte("[core]"),
		"passwords.secret":      []byte("secret data"),
		"ignored_dir/file.txt": []byte("should be skipped"),
	}

	adapter, fixture := f.Create(t, initialFiles, opts)
	defer fixture.Cleanup()

	store := vfs.NewMemStore()
	w := vfs.NewWorld(store)

	if err := adapter.Load(context.Background(), w); err != nil {
		t.Fatalf("[%s] Load failed: %v", f.Name(), err)
	}

	// main.go must be loaded
	if _, err := w.ReadFile("main.go"); err != nil {
		t.Fatalf("[%s] main.go should have been loaded: %v", f.Name(), err)
	}

	// excluded files must NOT be loaded
	for _, excluded := range []string{".git/config", "passwords.secret", "ignored_dir/file.txt"} {
		if _, err := w.ReadFile(excluded); err == nil {
			t.Errorf("[%s] expected %s to be excluded from loaded world", f.Name(), excluded)
		}
	}

	// Verify Fidelity documents exclusion patterns (FR-7.2)
	limitations := strings.Join(adapter.Fidelity().Limitations, " ")
	if !strings.Contains(limitations, "FR-7.2") && !strings.Contains(strings.ToLower(limitations), "exclude") {
		t.Errorf("[%s] Fidelity limitations missing exclude globs documentation (FR-7.2): %v", f.Name(), adapter.Fidelity().Limitations)
	}
}

// TestMaxFileSize verifies maximum file size boundaries (FR-7.3).
func TestMaxFileSize(t *testing.T, f AdapterFactory) {
	t.Helper()

	if !f.Capabilities().SupportsMaxFileSize {
		t.Skip("adapter does not support MaxFileSize")
	}

	limit := int64(128)
	opts := Options{
		MaxFileSize: limit,
	}

	initialFiles := map[string][]byte{
		"small.txt": []byte("small file"),
		"large.bin": make([]byte, 1024),
	}

	adapter, fixture := f.Create(t, initialFiles, opts)
	defer fixture.Cleanup()

	store := vfs.NewMemStore()
	w := vfs.NewWorld(store)

	if err := adapter.Load(context.Background(), w); err != nil {
		t.Fatalf("[%s] Load failed: %v", f.Name(), err)
	}

	// small.txt must be loaded
	if _, err := w.ReadFile("small.txt"); err != nil {
		t.Fatalf("[%s] small.txt should have been loaded: %v", f.Name(), err)
	}

	// large.bin must NOT be loaded
	if _, err := w.ReadFile("large.bin"); err == nil {
		t.Errorf("[%s] large.bin (> %d bytes) should have been skipped", f.Name(), limit)
	}

	// Verify Fidelity documents MaxFileSize (FR-7.3)
	limitations := strings.Join(adapter.Fidelity().Limitations, " ")
	if !strings.Contains(limitations, "FR-7.3") && !strings.Contains(limitations, fmt.Sprintf("%d", limit)) {
		t.Errorf("[%s] Fidelity limitations missing MaxFileSize documentation (FR-7.3): %v", f.Name(), adapter.Fidelity().Limitations)
	}
}

// TestFidelity verifies the adapter's Fidelity declaration matches its tier (FR-7.7).
func TestFidelity(t *testing.T, f AdapterFactory) {
	t.Helper()

	adapter, fixture := f.Create(t, nil, Options{})
	defer fixture.Cleanup()

	fid := adapter.Fidelity()

	// Must match expected tier
	if fid.Tier != f.ExpectedTier() {
		t.Fatalf("[%s] Fidelity tier mismatch: got %q, want %q", f.Name(), fid.Tier, f.ExpectedTier())
	}

	// Tier must be valid
	validTiers := map[source.Tier]bool{
		source.TierA: true,
		source.TierB: true,
		source.TierC: true,
	}
	if !validTiers[fid.Tier] {
		t.Fatalf("[%s] invalid fidelity tier: %q", f.Name(), fid.Tier)
	}

	// Limitations must not be empty
	if len(fid.Limitations) == 0 {
		t.Fatalf("[%s] Fidelity limitations must not be empty", f.Name())
	}

	for i, l := range fid.Limitations {
		if strings.TrimSpace(l) == "" {
			t.Errorf("[%s] limitation [%d] is empty or whitespace", f.Name(), i)
		}
	}
}

// TestContextCancellation verifies prompt cancellation when context is canceled.
func TestContextCancellation(t *testing.T, f AdapterFactory) {
	t.Helper()

	initialFiles := map[string][]byte{
		"a.txt": []byte("content"),
	}

	adapter, fixture := f.Create(t, initialFiles, Options{})
	defer fixture.Cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := vfs.NewMemStore()
	w := vfs.NewWorld(store)

	if err := adapter.Load(ctx, w); err == nil {
		t.Fatalf("[%s] Load with canceled context must return error, got nil", f.Name())
	}

	w2 := vfs.NewWorld(store)
	_ = w2.WriteFile("b.txt", []byte("b"), 0644)
	if err := adapter.Apply(ctx, w2); err == nil {
		t.Fatalf("[%s] Apply with canceled context must return error, got nil", f.Name())
	}
}
