package daytona_test

import (
	"os"
	"path"
	"testing"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/source/conformance"
	"github.com/mindfire/zygote/source/daytona"
)

type daytonaFactory struct{}

func (f *daytonaFactory) Name() string {
	return "daytona"
}

func (f *daytonaFactory) ExpectedTier() source.Tier {
	return source.TierB
}

func (f *daytonaFactory) Capabilities() conformance.Capabilities {
	return conformance.Capabilities{
		PreservesPermissionsOnLoad:  false,
		PreservesPermissionsOnApply: false,
		SupportsExclusionGlobs:      true,
		SupportsMaxFileSize:         true,
	}
}

func (f *daytonaFactory) Create(t *testing.T, initialFiles map[string][]byte, opts conformance.Options) (source.Source, conformance.Fixture) {
	t.Helper()
	fs := newFakeFS()

	for rel, content := range initialFiles {
		full := path.Join("/work", rel)
		fs.files[full] = content
	}

	adapter := daytona.NewWithFileSystem(fs, "/work")
	if opts.MaxFileSize > 0 {
		adapter.MaxFileSize = opts.MaxFileSize
	}
	if opts.ExcludeGlobs != nil {
		adapter.ExcludeGlobs = opts.ExcludeGlobs
	}

	return adapter, &daytonaFixture{fs: fs, root: "/work"}
}

type daytonaFixture struct {
	fs   *fakeFS
	root string
}

func (fix *daytonaFixture) ReadFile(_ *testing.T, relPath string) ([]byte, error) {
	full := path.Join(fix.root, relPath)
	data, ok := fix.fs.files[full]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (fix *daytonaFixture) FileExists(_ *testing.T, relPath string) bool {
	full := path.Join(fix.root, relPath)
	_, ok := fix.fs.files[full]
	return ok
}

func (fix *daytonaFixture) GetFileMode(_ *testing.T, _ string) (os.FileMode, error) {
	return 0644, nil
}

func (fix *daytonaFixture) RootDir() string {
	return fix.root
}

func (fix *daytonaFixture) Cleanup() {}

func TestDaytona_Conformance(t *testing.T) {
	conformance.RunSuite(t, &daytonaFactory{})
}
