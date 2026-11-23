package localdir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/source/conformance"
	"github.com/mindfire/zygote/source/localdir"
)

type localDirFactory struct{}

func (f *localDirFactory) Name() string {
	return "localdir"
}

func (f *localDirFactory) ExpectedTier() source.Tier {
	return source.TierA
}

func (f *localDirFactory) Capabilities() conformance.Capabilities {
	return conformance.Capabilities{
		PreservesPermissionsOnLoad:  true,
		PreservesPermissionsOnApply: true,
		SupportsExclusionGlobs:      true,
		SupportsMaxFileSize:         true,
	}
}

func (f *localDirFactory) Create(t *testing.T, initialFiles map[string][]byte, opts conformance.Options) (source.Source, conformance.Fixture) {
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

	adapter := localdir.New(dir)
	if opts.MaxFileSize > 0 {
		adapter.MaxFileSize = opts.MaxFileSize
	}
	if opts.ExcludeGlobs != nil {
		adapter.ExcludeGlobs = opts.ExcludeGlobs
	}

	return adapter, &localDirFixture{dir: dir}
}

type localDirFixture struct {
	dir string
}

func (fix *localDirFixture) ReadFile(_ *testing.T, relPath string) ([]byte, error) {
	return os.ReadFile(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
}

func (fix *localDirFixture) FileExists(_ *testing.T, relPath string) bool {
	_, err := os.Stat(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
	return err == nil
}

func (fix *localDirFixture) GetFileMode(_ *testing.T, relPath string) (os.FileMode, error) {
	fi, err := os.Stat(filepath.Join(fix.dir, filepath.FromSlash(relPath)))
	if err != nil {
		return 0, err
	}
	return fi.Mode(), nil
}

func (fix *localDirFixture) RootDir() string {
	return fix.dir
}

func (fix *localDirFixture) Cleanup() {}

func TestLocalDir_Conformance(t *testing.T) {
	conformance.RunSuite(t, &localDirFactory{})
}
