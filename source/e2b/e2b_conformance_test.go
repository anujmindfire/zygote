package e2b_test

import (
	"os"
	"path"
	"testing"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/source/conformance"
)

type e2bFactory struct{}

func (f *e2bFactory) Name() string {
	return "e2b"
}

func (f *e2bFactory) ExpectedTier() source.Tier {
	return source.TierB
}

func (f *e2bFactory) Capabilities() conformance.Capabilities {
	return conformance.Capabilities{
		PreservesPermissionsOnLoad:  false,
		PreservesPermissionsOnApply: false,
		SupportsExclusionGlobs:      true,
		SupportsMaxFileSize:         true,
	}
}

func (f *e2bFactory) Create(t *testing.T, initialFiles map[string][]byte, opts conformance.Options) (source.Source, conformance.Fixture) {
	t.Helper()
	fake := newFake(t)

	for rel, content := range initialFiles {
		full := path.Join("/work", rel)
		fake.files[full] = content
	}

	adapter := fake.adapter(t, "/work")
	if opts.MaxFileSize > 0 {
		adapter.MaxFileSize = opts.MaxFileSize
	}
	if opts.ExcludeGlobs != nil {
		adapter.ExcludeGlobs = opts.ExcludeGlobs
	}

	return adapter, &e2bFixture{fake: fake, root: "/work"}
}

type e2bFixture struct {
	fake *fakeSandbox
	root string
}

func (fix *e2bFixture) ReadFile(_ *testing.T, relPath string) ([]byte, error) {
	fix.fake.mu.Lock()
	defer fix.fake.mu.Unlock()

	full := path.Join(fix.root, relPath)
	data, ok := fix.fake.files[full]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (fix *e2bFixture) FileExists(_ *testing.T, relPath string) bool {
	fix.fake.mu.Lock()
	defer fix.fake.mu.Unlock()

	full := path.Join(fix.root, relPath)
	_, ok := fix.fake.files[full]
	return ok
}

func (fix *e2bFixture) GetFileMode(_ *testing.T, _ string) (os.FileMode, error) {
	return 0644, nil
}

func (fix *e2bFixture) RootDir() string {
	return fix.root
}

func (fix *e2bFixture) Cleanup() {}

func TestE2B_Conformance(t *testing.T) {
	conformance.RunSuite(t, &e2bFactory{})
}
