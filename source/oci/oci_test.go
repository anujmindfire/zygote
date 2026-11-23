package oci_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/source/oci"
	"github.com/mindfire/zygote/vfs"
)

var _ source.Source = (*oci.Adapter)(nil)

func TestNew_EmptyPath(t *testing.T) {
	_, err := oci.New("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestNew_BundleWithRootfsSubdir(t *testing.T) {
	bundleDir := t.TempDir()
	rootfsDir := filepath.Join(bundleDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0755); err != nil {
		t.Fatal(err)
	}

	a, err := oci.New(bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Rootfs != rootfsDir {
		t.Fatalf("expected rootfs %s, got %s", rootfsDir, a.Rootfs)
	}
}

func TestFidelity_TierC(t *testing.T) {
	a, err := oci.New(t.TempDir(), oci.WithExcludeGlobs("*.secret"), oci.WithMaxFileSize(100), oci.WithExposePaths("app"))
	if err != nil {
		t.Fatal(err)
	}
	f := a.Fidelity()
	if f.Tier != source.TierC {
		t.Fatalf("expected Tier C, got %s", f.Tier)
	}
	joined := strings.Join(f.Limitations, " ")
	if !strings.Contains(joined, "FR-7.2") {
		t.Errorf("expected FR-7.2 in limitations: %s", joined)
	}
	if !strings.Contains(joined, "FR-7.3") {
		t.Errorf("expected FR-7.3 in limitations: %s", joined)
	}
	if !strings.Contains(joined, "exposed container paths") {
		t.Errorf("expected exposed container paths in limitations: %s", joined)
	}
}

func TestRoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(srcDir, "app.py"), []byte("print('hello')"), 0644) //nolint:gosec

	store := vfs.NewMemStore()
	w := vfs.NewWorld(store)

	srcAdapter, err := oci.New(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := srcAdapter.Load(context.Background(), w); err != nil {
		t.Fatal(err)
	}

	data, err := w.ReadFile("app.py")
	if err != nil || string(data) != "print('hello')" {
		t.Fatalf("unexpected content: %s, %v", string(data), err)
	}

	_ = w.WriteFile("app.py", []byte("print('modified')"), 0644)
	_ = w.WriteFile("new.txt", []byte("brand new"), 0644)

	dstAdapter, err := oci.New(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := dstAdapter.Apply(context.Background(), w); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(filepath.Join(dstDir, "app.py"))
	if string(b) != "print('modified')" {
		t.Fatalf("expected modified content, got %s", string(b))
	}
	b, _ = os.ReadFile(filepath.Join(dstDir, "new.txt"))
	if string(b) != "brand new" {
		t.Fatalf("expected new file, got %s", string(b))
	}
}

func TestApply_RefusesPathEscape(t *testing.T) {
	dstDir := t.TempDir()
	store := vfs.NewMemStore()
	blobHash, _ := store.Put([]byte("evil"))

	maliciousTree := vfs.Tree{
		{
			Name: "../evil.txt",
			Kind: vfs.KindFile,
			Mode: 0644,
			Size: 4,
			Hash: blobHash,
		},
	}
	rootHash, _ := store.Put(maliciousTree.Encode())
	w := vfs.Fork(store, vfs.Snapshot{Root: rootHash})

	a, err := oci.New(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	err = a.Apply(context.Background(), w)
	if !errors.Is(err, oci.ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}
}
