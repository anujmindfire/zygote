// Package adapter_test verifies specification conformance of source adapters (FR-7.8).
package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/source/conformance"
	"github.com/mindfire/zygote/source/localdir"
	"github.com/mindfire/zygote/source/oci"
)

type specLocalDirFactory struct{}

func (f *specLocalDirFactory) Name() string { return "spec-localdir" }
func (f *specLocalDirFactory) ExpectedTier() source.Tier { return source.TierA }
func (f *specLocalDirFactory) Capabilities() conformance.Capabilities {
	return conformance.Capabilities{
		PreservesPermissionsOnLoad:  true,
		PreservesPermissionsOnApply: true,
		SupportsExclusionGlobs:      true,
		SupportsMaxFileSize:         true,
	}
}

func (f *specLocalDirFactory) Create(t *testing.T, initialFiles map[string][]byte, opts conformance.Options) (source.Source, conformance.Fixture) {
	t.Helper()
	dir := t.TempDir()

	for rel, content := range initialFiles {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		perm := os.FileMode(0644)
		if strings.HasSuffix(rel, ".sh") {
			perm = 0755
		}
		if err := os.WriteFile(target, content, perm); err != nil {
			t.Fatalf("failed to write %s: %v", rel, err)
		}
	}

	adapter := localdir.New(dir)
	if opts.MaxFileSize > 0 {
		adapter.MaxFileSize = opts.MaxFileSize
	}
	if opts.ExcludeGlobs != nil {
		adapter.ExcludeGlobs = opts.ExcludeGlobs
	}
	return adapter, &specFixture{dir: dir}
}

type specOCIFactory struct{}

func (f *specOCIFactory) Name() string { return "spec-oci" }
func (f *specOCIFactory) ExpectedTier() source.Tier { return source.TierC }
func (f *specOCIFactory) Capabilities() conformance.Capabilities {
	return conformance.Capabilities{
		PreservesPermissionsOnLoad:  true,
		PreservesPermissionsOnApply: true,
		SupportsExclusionGlobs:      true,
		SupportsMaxFileSize:         true,
	}
}

func (f *specOCIFactory) Create(t *testing.T, initialFiles map[string][]byte, opts conformance.Options) (source.Source, conformance.Fixture) {
	t.Helper()
	dir := t.TempDir()

	for rel, content := range initialFiles {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
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
	return adapter, &specFixture{dir: dir}
}

type specFixture struct {
	dir string
}

func (fix *specFixture) ReadFile(_ *testing.T, relPath string) ([]byte, error) {
	return os.ReadFile(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
}

func (fix *specFixture) FileExists(_ *testing.T, relPath string) bool {
	_, err := os.Stat(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
	return err == nil
}

func (fix *specFixture) GetFileMode(_ *testing.T, relPath string) (os.FileMode, error) {
	fi, err := os.Stat(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
	if err != nil {
		return 0, err
	}
	return fi.Mode(), nil
}

func (fix *specFixture) RootDir() string {
	return fix.dir
}

func (fix *specFixture) Cleanup() {}

func TestSpecConformance_LocalDir(t *testing.T) {
	conformance.RunSuite(t, &specLocalDirFactory{})
}

func TestSpecConformance_OCI(t *testing.T) {
	conformance.RunSuite(t, &specOCIFactory{})
}
