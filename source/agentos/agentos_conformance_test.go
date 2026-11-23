package agentos

import (
	"os"
	"path"
	"testing"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/source/conformance"
)

type agentOSFactory struct{}

func (f *agentOSFactory) Name() string {
	return "agentos"
}

func (f *agentOSFactory) ExpectedTier() source.Tier {
	return source.TierB
}

func (f *agentOSFactory) Capabilities() conformance.Capabilities {
	return conformance.Capabilities{
		PreservesPermissionsOnLoad:  false,
		PreservesPermissionsOnApply: false,
		SupportsExclusionGlobs:      true,
		SupportsMaxFileSize:         true,
	}
}

func (f *agentOSFactory) Create(t *testing.T, initialFiles map[string][]byte, opts conformance.Options) (source.Source, conformance.Fixture) {
	t.Helper()
	vm, a := newFakeVM(t)

	for rel, content := range initialFiles {
		full := path.Join("/home/agentos", rel)
		vm.files[full] = content
	}

	if opts.MaxFileSize > 0 {
		a.MaxFileSize = opts.MaxFileSize
	}
	if opts.ExcludeGlobs != nil {
		a.ExcludeGlobs = opts.ExcludeGlobs
	}

	return a, &agentOSFixture{vm: vm, root: "/home/agentos"}
}

type agentOSFixture struct {
	vm   *fakeVM
	root string
}

func (fix *agentOSFixture) ReadFile(_ *testing.T, relPath string) ([]byte, error) {
	fix.vm.mu.Lock()
	defer fix.vm.mu.Unlock()

	full := path.Join(fix.root, relPath)
	data, ok := fix.vm.files[full]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (fix *agentOSFixture) FileExists(_ *testing.T, relPath string) bool {
	fix.vm.mu.Lock()
	defer fix.vm.mu.Unlock()

	full := path.Join(fix.root, relPath)
	_, ok := fix.vm.files[full]
	return ok
}

func (fix *agentOSFixture) GetFileMode(_ *testing.T, _ string) (os.FileMode, error) {
	return 0644, nil
}

func (fix *agentOSFixture) RootDir() string {
	return fix.root
}

func (fix *agentOSFixture) Cleanup() {}

func TestAgentOS_Conformance(t *testing.T) {
	conformance.RunSuite(t, &agentOSFactory{})
}
