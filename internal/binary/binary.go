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
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
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

// Scoped binds a store to one execution, which is the shape an executor sees.
//
// Scoping happens in the runtime rather than at the call site so a node cannot
// read another tenant's payload even while holding its reference: the scope is
// not a parameter an executor can change.
type Scoped struct {
	store Store
	scope Scope
}

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
		return workflow.BinaryRef{}, fmt.Errorf("binary storage is not configured on this server")
	}
	return scoped.store.Put(scoped.scope, name, mediaType, body)
}

// Get reads a payload within this execution's scope.
func (scoped *Scoped) Get(id string) (io.ReadCloser, workflow.BinaryRef, error) {
	if scoped == nil || scoped.store == nil {
		return nil, workflow.BinaryRef{}, fmt.Errorf("binary storage is not configured on this server")
	}
	return scoped.store.Get(scoped.scope, id)
}
