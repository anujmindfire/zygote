// Package conformance provides a reusable conformance test suite for Source adapters (FR-7.8).
//
// Any runtime vendor or sandbox author implementing source.Source can run their adapter
// against this suite to guarantee behavioral compliance with Zygote's flight recorder specification:
//   - Correct Load(ctx, w) and Apply(ctx, w) roundtrip semantics.
//   - Strict rejection of directory traversals (..) and path escapes (FR-7.5, NFR-4.1).
//   - Correct handling of file permissions, exclusion patterns, and MaxFileSize boundaries (FR-7.2, FR-7.3).
//   - Honest Fidelity() declarations matching Tier A, Tier B, or Tier C (FR-7.7).
//   - Context cancellation responsiveness.
package conformance

import (
	"os"
	"testing"

	"github.com/mindfire/zygote/source"
)

// Options specifies configuration parameters for the adapter under test.
type Options struct {
	MaxFileSize  int64
	ExcludeGlobs []string
}

// Capabilities declares optional or tier-specific features supported by an adapter.
type Capabilities struct {
	// PreservesPermissionsOnLoad indicates that Load faithfully records exact POSIX file permissions.
	PreservesPermissionsOnLoad bool
	// PreservesPermissionsOnApply indicates that Apply faithfully sets POSIX file permissions on disk/sandbox.
	PreservesPermissionsOnApply bool
	// SupportsExclusionGlobs indicates that the adapter honors custom exclusion patterns.
	SupportsExclusionGlobs bool
	// SupportsMaxFileSize indicates that the adapter honors custom MaxFileSize limits.
	SupportsMaxFileSize bool
}

// Fixture provides test inspection and lifecycle control over the adapter's backing environment.
type Fixture interface {
	// ReadFile reads the content of a file from the backing environment by relative path.
	ReadFile(t *testing.T, relPath string) ([]byte, error)

	// FileExists reports whether a file exists in the backing environment.
	FileExists(t *testing.T, relPath string) bool

	// GetFileMode returns the file mode / permissions of a file in the backing environment.
	GetFileMode(t *testing.T, relPath string) (os.FileMode, error)

	// RootDir returns the root directory or base path of the environment if applicable.
	RootDir() string

	// Cleanup tears down any temporary resources allocated for this test.
	Cleanup()
}

// AdapterFactory abstracts how an adapter and its backing environment are instantiated.
type AdapterFactory interface {
	// Name returns the identifier of the adapter (e.g. "localdir", "agentos", "e2b", "daytona", "oci").
	Name() string

	// ExpectedTier returns the expected Fidelity tier (TierA, TierB, TierC).
	ExpectedTier() source.Tier

	// Capabilities returns the feature flags for this adapter.
	Capabilities() Capabilities

	// Create initializes a new adapter and backing environment populated with initialFiles.
	Create(t *testing.T, initialFiles map[string][]byte, opts Options) (source.Source, Fixture)
}

// RunSuite runs the entire conformance test suite against the adapter factory.
func RunSuite(t *testing.T, f AdapterFactory) {
	t.Helper()

	t.Run("LoadAndApply_RoundTrip", func(t *testing.T) {
		TestLoadAndApplyRoundTrip(t, f)
	})

	t.Run("Apply_RejectsPathTraversal", func(t *testing.T) {
		TestPathTraversal(t, f)
	})

	t.Run("Honors_FilePermissions", func(t *testing.T) {
		TestFilePermissions(t, f)
	})

	t.Run("Honors_ExclusionPatterns", func(t *testing.T) {
		TestExclusionPatterns(t, f)
	})

	t.Run("Honors_MaxFileSize", func(t *testing.T) {
		TestMaxFileSize(t, f)
	})

	t.Run("Fidelity_ValidDeclaration", func(t *testing.T) {
		TestFidelity(t, f)
	})

	t.Run("Context_Cancellation", func(t *testing.T) {
		TestContextCancellation(t, f)
	})
}
