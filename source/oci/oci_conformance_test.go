package oci_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/source/conformance"
	"github.com/mindfire/zygote/source/oci"
)

type ociFactory struct{}

func (f *ociFactory) Name() string {
	return "oci"
}

func (f *ociFactory) ExpectedTier() source.Tier {
	return source.TierC
}

func (f *ociFactory) Capabilities() conformance.Capabilities {
	return conformance.Capabilities{
		PreservesPermissionsOnLoad:  true,
		PreservesPermissionsOnApply: true,
		SupportsExclusionGlobs:      true,
		SupportsMaxFileSize:         true,
	}
}

func (f *ociFactory) Create(t *testing.T, initialFiles map[string][]byte, opts conformance.Options) (source.Source, conformance.Fixture) {
	t.Helper()
	dir := t.TempDir()

	for rel, content := range initialFiles {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", rel, err)
		}
		perm := os.FileMode(0644)
		if strings.HasSuffix(rel, ".sh") {
			perm = 0755
		}
		if err := os.WriteFile(target, content, perm); err != nil {
			t.Fatalf("failed to write %s: %v", rel, err)
		}
	}

	adapter, err := oci.New(dir)
	if err != nil {
		t.Fatalf("failed to create oci adapter: %v", err)
	}

	if opts.MaxFileSize > 0 {
		adapter.MaxFileSize = opts.MaxFileSize
	}
	if opts.ExcludeGlobs != nil {
		adapter.ExcludeGlobs = opts.ExcludeGlobs
	}

	return adapter, &ociFixture{dir: dir}
}

type ociFixture struct {
	dir string
}

func (fix *ociFixture) ReadFile(_ *testing.T, relPath string) ([]byte, error) {
	return os.ReadFile(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
}

func (fix *ociFixture) FileExists(_ *testing.T, relPath string) bool {
	_, err := os.Stat(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
	return err == nil
}

func (fix *ociFixture) GetFileMode(_ *testing.T, relPath string) (os.FileMode, error) {
	fi, err := os.Stat(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
	if err != nil {
		return 0, err
	}
	return fi.Mode(), nil
}

func (fix *ociFixture) RootDir() string {
	return fix.dir
}

func (fix *ociFixture) Cleanup() {}

func TestOCI_Conformance(t *testing.T) {
	conformance.RunSuite(t, &ociFactory{})
}
