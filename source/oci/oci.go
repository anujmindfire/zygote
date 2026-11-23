// Package oci provides a Source adapter for OCI container bundles and rootfs.
package oci

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mindfire/zygote/source"
	"github.com/mindfire/zygote/vfs"
)

// ErrPathEscape indicates traversal.
var ErrPathEscape = source.ErrPathEscape

var _ source.Source = (*Adapter)(nil)

// Option configures an Adapter.
type Option func(*Adapter)

// WithMaxFileSize sets the maximum file size to capture.
func WithMaxFileSize(size int64) Option {
	return func(a *Adapter) { a.MaxFileSize = size }
}

// WithExcludeGlobs sets file and directory exclusion patterns.
func WithExcludeGlobs(globs ...string) Option {
	return func(a *Adapter) { a.ExcludeGlobs = globs }
}

// WithExposePaths restricts captured files to specific container exposed paths (Tier C).
func WithExposePaths(paths ...string) Option {
	return func(a *Adapter) { a.ExposePaths = paths }
}

// Adapter implements Source for an OCI container bundle or container rootfs.
type Adapter struct {
	Rootfs        string
	MaxFileSize   int64
	ExcludeGlobs  []string
	ExcludedPaths []string
	ExposePaths   []string
}

// New creates an OCI source adapter for a container bundle or rootfs directory.
func New(bundleOrRootfs string, opts ...Option) (*Adapter, error) {
	if strings.TrimSpace(bundleOrRootfs) == "" {
		return nil, errors.New("oci: bundle or rootfs path cannot be empty")
	}

	rootfs := bundleOrRootfs
	// Check if this is an OCI bundle containing a rootfs directory
	bundleRootfs := filepath.Join(bundleOrRootfs, "rootfs")
	if fi, err := os.Stat(bundleRootfs); err == nil && fi.IsDir() {
		rootfs = bundleRootfs
	}

	a := &Adapter{
		Rootfs:       rootfs,
		MaxFileSize:  10 * 1024 * 1024,
		ExcludeGlobs: []string{".git", "node_modules", "__pycache__", "proc", "sys", "dev"},
	}

	for _, opt := range opts {
		opt(a)
	}

	return a, nil
}

func (a *Adapter) shouldExclude(name string) bool {
	for _, g := range a.ExcludeGlobs {
		if matched, _ := filepath.Match(g, name); matched {
			return true
		}
	}
	return false
}

func (a *Adapter) isExposed(rel string) bool {
	if len(a.ExposePaths) == 0 {
		return true
	}
	cleanRel := filepath.Clean(rel)
	for _, exp := range a.ExposePaths {
		cleanExp := filepath.Clean(exp)
		if cleanRel == cleanExp || strings.HasPrefix(cleanRel, cleanExp+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Fidelity declares Tier C fidelity matching container runtime exposure (FR-7.7).
func (a *Adapter) Fidelity() source.Fidelity {
	limitations := []string{
		"captures container filesystem state exposed by runtime (Tier C, FR-7.7)",
		"pseudo-filesystems (/proc, /sys, /dev) and device nodes are excluded",
		"transient container runtime and kernel memory state are not captured",
		fmt.Sprintf("files larger than %d bytes are skipped (FR-7.3)", a.MaxFileSize),
	}
	if len(a.ExcludeGlobs) > 0 {
		limitations = append(limitations,
			fmt.Sprintf("paths matching exclude globs are skipped (FR-7.2): %s", strings.Join(a.ExcludeGlobs, ", ")))
	}
	if len(a.ExposePaths) > 0 {
		limitations = append(limitations,
			fmt.Sprintf("only exposed container paths are captured: %s", strings.Join(a.ExposePaths, ", ")))
	}
	return source.Fidelity{Tier: source.TierC, Limitations: limitations}
}

// Load loads the exposed container rootfs into the VFS world.
func (a *Adapter) Load(ctx context.Context, w *vfs.World) error {
	a.ExcludedPaths = nil

	return filepath.WalkDir(a.Rootfs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if a.shouldExclude(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(a.Rootfs, path)
		if err != nil {
			return err
		}
		if !a.isExposed(rel) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			a.ExcludedPaths = append(a.ExcludedPaths, path)
			return nil
		}

		if info.Size() > a.MaxFileSize {
			a.ExcludedPaths = append(a.ExcludedPaths, path)
			return nil
		}

		data, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return err
		}

		slashRel := filepath.ToSlash(rel)
		return w.WriteFile(slashRel, data, uint32(info.Mode().Perm())) //nolint:gosec
	})
}

// Apply writes the world's files into the container rootfs.
func (a *Adapter) Apply(ctx context.Context, w *vfs.World) error {
	absRoot, err := filepath.Abs(a.Rootfs)
	if err != nil {
		return err
	}

	return fs.WalkDir(w, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if path == "." || d.IsDir() {
			return nil
		}

		target := filepath.Join(absRoot, filepath.FromSlash(path))
		cleanTarget := filepath.Clean(target)

		// Strict path traversal check (FR-7.5, NFR-4.1)
		if !strings.HasPrefix(cleanTarget, absRoot+string(filepath.Separator)) && cleanTarget != absRoot {
			return ErrPathEscape
		}

		data, err := fs.ReadFile(w, path)
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { //nolint:gosec
			return err
		}

		return os.WriteFile(target, data, info.Mode().Perm()) //nolint:gosec
	})
}
