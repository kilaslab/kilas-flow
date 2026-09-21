// Package binary stores the payloads a workflow's items refer to.
//
// The item contract has had a binary half since V1 and nothing implemented it:
// `workflow.BinaryRef` was `{ID, FileName, MediaType, Size}` under a comment
// saying payload storage was out of scope, and the whole lifecycle of the field
// was two clone functions copying a map nothing ever filled.
//
// Payloads live on a filesystem root rather than in the internal database.
// Multi-megabyte WhatsApp media in SQLite rows would bloat the file this
// product ships as its default, and under the PostgreSQL tier it would land in
// a table inside a customer's own shared database — exactly the posture that
// phase is trying to keep narrow. A root directory is also the honest shape for
// the object-storage backend a hosted deployment will want behind this
// interface.
package binary

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// ErrNotFound reports a reference the store does not hold.
var ErrNotFound = errors.New("binary payload not found")

// ErrTooLarge reports a payload above the configured bound.
var ErrTooLarge = errors.New("binary payload exceeds the configured limit")

// Store keeps payloads out of workflow documents, execution records and API
// responses. Only a reference travels through those.
type Store interface {
	// Put writes a payload and returns the reference an item carries.
	Put(scope Scope, name, mediaType string, body io.Reader) (workflow.BinaryRef, error)
	// Get reads a payload back. A reference from another tenant or another
	// execution does not resolve.
	Get(scope Scope, id string) (io.ReadCloser, workflow.BinaryRef, error)
	// DeleteExecution removes everything one execution wrote.
	DeleteExecution(scope Scope) error
	// DeleteTenant removes every payload one tenant owns, and only those.
	DeleteTenant(tenantID string) (TenantResult, error)
}

// TenantResult names what deleting one tenant's payloads removed. It is
// evidence for a deletion request, which is honoured only when the caller can
// see that it was.
type TenantResult struct {
	// Executions is how many top-level directories the tenant had, one per
	// execution that ever stored a payload.
	Executions int
	// Files and Bytes are the regular files below them. Bytes is what a disk
	// actually gives back, which is the number an operator asks about.
	Files int
	Bytes int64
}

// Scope is whose payload this is.
//
// Both fields are part of the key, not a filter applied afterwards: a reference
// is an opaque ID that travels in an execution record, and resolving one
// without its scope would let any tenant read any payload by guessing.
type Scope struct {
	TenantID    string
	ExecutionID string
}

func (scope Scope) validate() error {
	if !safeSegment.MatchString(scope.TenantID) {
		return fmt.Errorf("binary scope needs a tenant")
	}
	if !safeSegment.MatchString(scope.ExecutionID) {
		return fmt.Errorf("binary scope needs an execution")
	}
	return nil
}

// safeSegment is what may appear in a path segment.
//
// The tenant and execution come from inside this process, but the reference ID
// travels through an execution record and back, so all three are checked the
// same way rather than trusting provenance — one of them becoming
// attacker-shaped later should not become a path traversal.
var safeSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// FileStore is the filesystem-backed implementation.
type FileStore struct {
	root     string
	maxBytes int64
}

var _ Store = (*FileStore)(nil)

// NewFileStore roots a store at a directory.
func NewFileStore(root string, maxBytes int64) (*FileStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("a binary store needs a root directory")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("a binary store needs a positive size limit")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create binary store root: %w", err)
	}
	return &FileStore{root: root, maxBytes: maxBytes}, nil
}

// Put writes a payload, refusing anything over the bound.
//
// The limit is enforced while reading rather than from a declared length: a
// caller that lies about the size, or a stream whose length is unknown, would
// otherwise fill the disk. Reading one byte past the limit is what
// distinguishes "exactly at the bound" from "over it".
func (store *FileStore) Put(scope Scope, name, mediaType string, body io.Reader) (workflow.BinaryRef, error) {
	if err := scope.validate(); err != nil {
		return workflow.BinaryRef{}, err
	}
	id, err := workflow.NewID("bin")
	if err != nil {
		return workflow.BinaryRef{}, err
	}
	directory := store.directory(scope)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return workflow.BinaryRef{}, fmt.Errorf("create binary directory: %w", err)
	}

	path := filepath.Join(directory, id)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return workflow.BinaryRef{}, fmt.Errorf("create binary payload: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(body, store.maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return workflow.BinaryRef{}, fmt.Errorf("write binary payload: %w", copyErr)
		}
		return workflow.BinaryRef{}, fmt.Errorf("write binary payload: %w", closeErr)
	}
	if written > store.maxBytes {
		// Removed rather than truncated: half a media file that reports success
		// is worse than a refusal.
		_ = os.Remove(path)
		return workflow.BinaryRef{}, fmt.Errorf("%w of %d bytes", ErrTooLarge, store.maxBytes)
	}

	return workflow.BinaryRef{ID: id, FileName: name, MediaType: mediaType, Size: written}, nil
}

// Get reads a payload back within its scope.
func (store *FileStore) Get(scope Scope, id string) (io.ReadCloser, workflow.BinaryRef, error) {
	if err := scope.validate(); err != nil {
		return nil, workflow.BinaryRef{}, err
	}
	if !safeSegment.MatchString(id) {
		return nil, workflow.BinaryRef{}, ErrNotFound
	}
	path := filepath.Join(store.directory(scope), id)
	info, err := os.Stat(path)
	if err != nil {
		return nil, workflow.BinaryRef{}, ErrNotFound
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, workflow.BinaryRef{}, ErrNotFound
	}
	return file, workflow.BinaryRef{ID: id, Size: info.Size()}, nil
}

// DeleteExecution removes everything one execution wrote.
//
// A binary store with no deletion path is a disk-full incident with a delay
// fuse. Execution pruning does not exist yet, so this is wired to whatever
// removes an execution today — when retention arrives it has one function to
// call rather than a directory tree to reverse-engineer.
func (store *FileStore) DeleteExecution(scope Scope) error {
	if err := scope.validate(); err != nil {
		return err
	}
	if err := os.RemoveAll(store.directory(scope)); err != nil {
		return fmt.Errorf("remove binary payloads: %w", err)
	}
	return nil
}

func (store *FileStore) directory(scope Scope) string {
	return filepath.Join(store.root, scope.TenantID, scope.ExecutionID)
}

// DeleteTenant removes every payload one tenant owns.
//
// The tenant's payloads are exactly the directory named after it, because Put
// composes <root>/<tenant>/<execution>/<payload> and never creates anything
// else. What that makes easy to get wrong is the locating: the tenant id is
// matched against the entries the root actually holds rather than joined onto
// the root, because safeSegment allows uppercase and the filesystem this
// product is most often developed on is case-insensitive, so a join would let
// "ACME" remove tenant acme's payloads while every row naming acme survives in
// the database.
//
// The counts are taken before the removal, without following symlinks: a link
// at <root>/<tenant> is removed as the link it is and whatever it points at is
// left alone, which is the only reading of "delete this tenant's payloads"
// that cannot escape the store.
func (store *FileStore) DeleteTenant(tenantID string) (TenantResult, error) {
	if strings.TrimSpace(tenantID) == "" {
		// Not an empty directory but the root itself: filepath.Join(root, "")
		// is root, and removing it would take every tenant's payloads with it.
		return TenantResult{}, errors.New("binary: a tenant id is required to delete payloads")
	}
	if !safeSegment.MatchString(tenantID) {
		// Put refuses an id that fails this predicate, so no payload can exist
		// under a directory name that would fail it either. Nothing to remove,
		// and no path is ever composed from an id that was never a segment.
		return TenantResult{}, nil
	}

	entries, err := os.ReadDir(store.root)
	if err != nil {
		if os.IsNotExist(err) {
			return TenantResult{}, nil
		}
		return TenantResult{}, fmt.Errorf("read binary store root: %w", err)
	}
	var matched string
	for _, entry := range entries {
		if entry.Name() == tenantID {
			matched = entry.Name()
			break
		}
	}
	if matched == "" {
		// A tenant with nothing on disk purges to zero rather than failing, so
		// a retried deletion converges.
		return TenantResult{}, nil
	}

	path := filepath.Join(store.root, matched)
	var result TenantResult
	if info, err := os.Lstat(path); err != nil {
		return TenantResult{}, fmt.Errorf("inspect binary payload directory: %w", err)
	} else if info.IsDir() {
		counted, err := store.measure(path)
		if err != nil {
			return TenantResult{}, err
		}
		result = counted
	}
	if err := os.RemoveAll(path); err != nil {
		return TenantResult{}, fmt.Errorf("remove binary payloads: %w", err)
	}
	return result, nil
}

// measure counts one tenant directory: one top-level entry per execution, and
// the regular files and bytes below them.
//
// WalkDir reports a symlink as the link rather than descending into it, so a
// payload directory that was replaced by a link to somewhere else is removed
// without being counted — and, more to the point, without being read.
func (store *FileStore) measure(directory string) (TenantResult, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return TenantResult{}, fmt.Errorf("read binary payload directory: %w", err)
	}
	result := TenantResult{Executions: len(entries)}
	err = filepath.WalkDir(directory, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		result.Files++
		result.Bytes += info.Size()
		return nil
	})
	if err != nil {
		return TenantResult{}, fmt.Errorf("measure binary payloads: %w", err)
	}
	return result, nil
}

// Scoped binds a store to one execution, which is the shape an executor sees.
//
// Scoping happens in the runtime rather than at the call site so a node cannot
// read another tenant's payload even while holding its reference: the scope is
// not a parameter an executor can change.
type Scoped struct {
	store Store
	scope Scope
}

// ErrNotConfigured is what a node gets when this server has nowhere to put a
// payload.
//
// It names the setting and its environment variable because the node that
// needed storage is rarely the place the mistake was made: the operator set the
// root wrong at boot, or never set it, and the error appears much later inside
// a workflow run. "Binary storage is not configured" alone sent people looking
// through the node's own configuration.
var ErrNotConfigured = errors.New(
	"binary storage is not configured on this server: set binary.root (KILASFLOW_BINARY_ROOT) to a writable directory")

// For returns a store bound to one execution.
func For(store Store, tenantID, executionID string) *Scoped {
	if store == nil {
		return nil
	}
	return &Scoped{store: store, scope: Scope{TenantID: tenantID, ExecutionID: executionID}}
}

// Put writes a payload within this execution's scope.
func (scoped *Scoped) Put(name, mediaType string, body io.Reader) (workflow.BinaryRef, error) {
	if scoped == nil || scoped.store == nil {
		return workflow.BinaryRef{}, ErrNotConfigured
	}
	return scoped.store.Put(scoped.scope, name, mediaType, body)
}

// Get reads a payload within this execution's scope.
func (scoped *Scoped) Get(id string) (io.ReadCloser, workflow.BinaryRef, error) {
	if scoped == nil || scoped.store == nil {
		return nil, workflow.BinaryRef{}, ErrNotConfigured
	}
	return scoped.store.Get(scoped.scope, id)
}
