package binary_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/binary"
)

func store(t *testing.T, maxBytes int64) (*binary.FileStore, string) {
	t.Helper()
	root := t.TempDir()
	created, err := binary.NewFileStore(root, maxBytes)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	return created, root
}

func TestPutReturnsAReferenceAndGetReadsThePayloadBack(t *testing.T) {
	t.Parallel()
	created, _ := store(t, 1024)
	scope := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-1"}

	reference, err := created.Put(scope, "receipt.pdf", "application/pdf", strings.NewReader("payload bytes"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if reference.ID == "" {
		t.Fatal("Put() returned no reference ID")
	}
	if reference.FileName != "receipt.pdf" || reference.MediaType != "application/pdf" {
		t.Fatalf("reference = %#v, want the name and media type preserved", reference)
	}
	if reference.Size != int64(len("payload bytes")) {
		t.Fatalf("Size = %d, want %d", reference.Size, len("payload bytes"))
	}

	body, read, err := created.Get(scope, reference.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer body.Close()
	contents, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read payload error = %v", err)
	}
	if string(contents) != "payload bytes" {
		t.Fatalf("payload = %q, want the bytes that were written", contents)
	}
	if read.Size != reference.Size {
		t.Fatalf("Get() Size = %d, want %d", read.Size, reference.Size)
	}
}

// The reference ID travels in an execution record, so a tenant that holds one
// must still not be able to read it. Scope is part of the key rather than a
// filter, and this is the test that says so.
func TestGetRefusesAReferenceFromAnotherTenantOrExecution(t *testing.T) {
	t.Parallel()
	created, _ := store(t, 1024)
	owner := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-1"}
	reference, err := created.Put(owner, "secret.png", "image/png", strings.NewReader("mine"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	for name, scope := range map[string]binary.Scope{
		"another tenant":    {TenantID: "tenant-b", ExecutionID: "exec-1"},
		"another execution": {TenantID: "tenant-a", ExecutionID: "exec-2"},
	} {
		if _, _, err := created.Get(scope, reference.ID); !errors.Is(err, binary.ErrNotFound) {
			t.Fatalf("Get() from %s error = %v, want ErrNotFound", name, err)
		}
	}
}

func TestGetRefusesAnIdentifierThatCouldEscapeTheRoot(t *testing.T) {
	t.Parallel()
	created, root := store(t, 1024)
	if err := os.WriteFile(filepath.Join(root, "outside"), []byte("not yours"), 0o600); err != nil {
		t.Fatalf("write fixture error = %v", err)
	}
	scope := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-1"}

	for _, id := range []string{"../../outside", "..", "a/b", ""} {
		if _, _, err := created.Get(scope, id); !errors.Is(err, binary.ErrNotFound) {
			t.Fatalf("Get(%q) error = %v, want ErrNotFound", id, err)
		}
	}
}

func TestPutRefusesAPayloadOverTheBoundAndLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	created, root := store(t, 8)
	scope := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-1"}

	if _, err := created.Put(scope, "big.bin", "application/octet-stream", bytes.NewReader(make([]byte, 9))); !errors.Is(err, binary.ErrTooLarge) {
		t.Fatalf("Put() over the bound error = %v, want ErrTooLarge", err)
	}
	// Removed rather than truncated: a partial file that reported success would
	// be indistinguishable from a real one.
	entries, err := os.ReadDir(filepath.Join(root, "tenant-a", "exec-1"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read execution directory error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("execution directory holds %d files, want none", len(entries))
	}

	// Exactly at the bound is allowed; one byte past it is not.
	if _, err := created.Put(scope, "exact.bin", "application/octet-stream", bytes.NewReader(make([]byte, 8))); err != nil {
		t.Fatalf("Put() at exactly the bound error = %v, want success", err)
	}
}

func TestDeleteExecutionRemovesOnlyThatExecution(t *testing.T) {
	t.Parallel()
	created, _ := store(t, 1024)
	doomed := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-1"}
	kept := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-2"}

	goneRef, err := created.Put(doomed, "a.txt", "text/plain", strings.NewReader("a"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	keptRef, err := created.Put(kept, "b.txt", "text/plain", strings.NewReader("b"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if err := created.DeleteExecution(doomed); err != nil {
		t.Fatalf("DeleteExecution() error = %v", err)
	}
	if _, _, err := created.Get(doomed, goneRef.ID); !errors.Is(err, binary.ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
	body, _, err := created.Get(kept, keptRef.ID)
	if err != nil {
		t.Fatalf("Get() of a sibling execution error = %v, want it to survive", err)
	}
	body.Close()

	// Deleting again is not an error: retention that crashes on a second pass
	// is retention that stops running.
	if err := created.DeleteExecution(doomed); err != nil {
		t.Fatalf("DeleteExecution() twice error = %v", err)
	}
}

// A tenant's payloads are exactly its directory under the root: one tenant
// deletion has to remove every execution it ever wrote, and nothing of the
// neighbour's. The byte count is part of the answer a deletion request is
// honoured with, so it is asserted rather than merely returned.
func TestDeleteTenantRemovesEveryExecutionOfThatTenantOnly(t *testing.T) {
	t.Parallel()
	created, root := store(t, 1024)
	doomed := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-1"}
	doomedTwo := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-2"}
	kept := binary.Scope{TenantID: "tenant-b", ExecutionID: "exec-1"}

	for _, payload := range []struct {
		scope binary.Scope
		body  string
	}{
		{doomed, "first payload"},
		{doomed, "second"},
		{doomedTwo, "third"},
	} {
		if _, err := created.Put(payload.scope, "a.txt", "text/plain", strings.NewReader(payload.body)); err != nil {
			t.Fatalf("Put(%+v) error = %v", payload.scope, err)
		}
	}
	keptRef, err := created.Put(kept, "b.txt", "text/plain", strings.NewReader("more of the neighbour's"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	removed, err := created.DeleteTenant("tenant-a")
	if err != nil {
		t.Fatalf("DeleteTenant() error = %v", err)
	}
	want := binary.TenantResult{Executions: 2, Files: 3, Bytes: int64(len("first payload") + len("second") + len("third"))}
	if removed != want {
		t.Errorf("DeleteTenant() = %+v, want %+v", removed, want)
	}
	if _, err := os.Stat(filepath.Join(root, "tenant-a")); !os.IsNotExist(err) {
		t.Errorf("tenant directory survives the delete: %v", err)
	}

	// The neighbour is still there through the store's own read path, which is
	// what its executor will use.
	body, _, err := created.Get(kept, keptRef.ID)
	if err != nil {
		t.Fatalf("Get(neighbour) after delete error = %v", err)
	}
	defer body.Close()
	contents, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read neighbour payload error = %v", err)
	}
	if string(contents) != "more of the neighbour's" {
		t.Errorf("neighbour payload = %q, want it untouched", contents)
	}
}

// An empty tenant is not a tenant. filepath.Join(root, "") is the root itself,
// so a delete that accepted "" would erase every tenant's payloads in one
// call — the highest-severity mistake this function can make.
func TestDeleteTenantRefusesAnEmptyTenantAndLeavesTheRootIntact(t *testing.T) {
	t.Parallel()
	created, root := store(t, 1024)
	scope := binary.Scope{TenantID: "tenant-b", ExecutionID: "exec-1"}
	reference, err := created.Put(scope, "a.txt", "text/plain", strings.NewReader("keep"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	for _, tenantID := range []string{"", "  ", "\t"} {
		if _, err := created.DeleteTenant(tenantID); err == nil {
			t.Errorf("DeleteTenant(%q) = nil error, want a refusal", tenantID)
		}
	}

	body, _, err := created.Get(scope, reference.ID)
	if err != nil {
		t.Fatalf("Get() after a refused delete error = %v", err)
	}
	body.Close()
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 {
		t.Errorf("root holds %d entries (%v), want the one tenant directory", len(entries), err)
	}
}

// An id Put refuses can have no payloads, so there is nothing to locate and
// nothing to compose a path from: a traversal-shaped id is a zero result, not
// an error and not a path.
func TestDeleteTenantIgnoresAnUnsafeSegment(t *testing.T) {
	t.Parallel()
	created, root := store(t, 1024)
	neighbour := binary.Scope{TenantID: "tenant-b", ExecutionID: "exec-1"}
	reference, err := created.Put(neighbour, "a.txt", "text/plain", strings.NewReader("keep"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	for _, tenantID := range []string{"../tenant-b", "..", "tenant/a", ".hidden"} {
		removed, err := created.DeleteTenant(tenantID)
		if err != nil {
			t.Errorf("DeleteTenant(%q) error = %v, want nil", tenantID, err)
		}
		if removed != (binary.TenantResult{}) {
			t.Errorf("DeleteTenant(%q) = %+v, want zero", tenantID, removed)
		}
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the root itself was touched: %v", err)
	}
	body, _, err := created.Get(neighbour, reference.ID)
	if err != nil {
		t.Fatalf("Get(neighbour) after unsafe deletes error = %v", err)
	}
	body.Close()
}

// The tenant id is matched against the directory's actual name, not resolved
// through the filesystem. safeSegment allows uppercase and macOS is
// case-insensitive by default, so a join would make "ACME" delete tenant
// acme's payloads while every database row still says acme.
func TestDeleteTenantMatchesTheDirectoryNameExactly(t *testing.T) {
	t.Parallel()
	created, root := store(t, 1024)
	scope := binary.Scope{TenantID: "acme", ExecutionID: "exec-1"}
	reference, err := created.Put(scope, "a.txt", "text/plain", strings.NewReader("acme's"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	removed, err := created.DeleteTenant("ACME")
	if err != nil {
		t.Fatalf("DeleteTenant(ACME) error = %v", err)
	}
	if removed != (binary.TenantResult{}) {
		t.Errorf("DeleteTenant(ACME) = %+v, want zero", removed)
	}
	body, _, err := created.Get(scope, reference.ID)
	if err != nil {
		t.Fatalf("Get(acme) after DeleteTenant(ACME) error = %v, want the payload intact", err)
	}
	body.Close()

	// A filesystem that makes "ACME" resolve to acme's directory is the only
	// one where the mistake above is possible at all, and it is worth saying
	// which one this ran on.
	if _, err := os.Stat(filepath.Join(root, "ACME")); err != nil {
		t.Log("this filesystem is case-sensitive, so a join would not have resolved ACME to acme here either")
	}

	// The matcher is not simply refusing everything: the exact name removes.
	removed, err = created.DeleteTenant("acme")
	if err != nil {
		t.Fatalf("DeleteTenant(acme) error = %v", err)
	}
	if removed.Executions != 1 || removed.Files != 1 {
		t.Errorf("DeleteTenant(acme) = %+v, want the one execution and one file", removed)
	}
}

// A symlink where a tenant directory should be is removed as the link it is:
// following it would delete whatever tree it points at, outside the store.
func TestDeleteTenantDoesNotFollowASymlink(t *testing.T) {
	t.Parallel()
	created, root := store(t, 1024)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("not the store's"), 0o600); err != nil {
		t.Fatalf("write fixture error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "tenant-a")); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}

	removed, err := created.DeleteTenant("tenant-a")
	if err != nil {
		t.Fatalf("DeleteTenant() error = %v", err)
	}
	if removed != (binary.TenantResult{}) {
		t.Errorf("DeleteTenant() = %+v, want zero: the link held no payloads of its own", removed)
	}
	if _, err := os.Lstat(filepath.Join(root, "tenant-a")); !os.IsNotExist(err) {
		t.Errorf("the link survives the delete: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(outside, "keep.txt")); err != nil || string(contents) != "not the store's" {
		t.Errorf("the link's target = (%q, %v), want it untouched", contents, err)
	}
}

// A deletion request that is retried must converge, and a directory that was
// already removed by hand must not wedge it.
func TestDeleteTenantIsIdempotent(t *testing.T) {
	t.Parallel()
	created, root := store(t, 1024)
	scope := binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-1"}
	if _, err := created.Put(scope, "a.txt", "text/plain", strings.NewReader("gone")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	first, err := created.DeleteTenant("tenant-a")
	if err != nil {
		t.Fatalf("DeleteTenant() error = %v", err)
	}
	if first.Executions != 1 || first.Files != 1 {
		t.Fatalf("DeleteTenant() = %+v, want the one execution and one file", first)
	}
	for range 2 {
		again, err := created.DeleteTenant("tenant-a")
		if err != nil {
			t.Fatalf("DeleteTenant() retry error = %v", err)
		}
		if again != (binary.TenantResult{}) {
			t.Errorf("DeleteTenant() retry = %+v, want zero", again)
		}
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("a retry removed the root: %v", err)
	}
}

func TestAStoreNeedsARootAndAPositiveBound(t *testing.T) {
	t.Parallel()
	if _, err := binary.NewFileStore("  ", 16); err == nil {
		t.Fatal("NewFileStore() with no root, want an error")
	}
	if _, err := binary.NewFileStore(t.TempDir(), 0); err == nil {
		t.Fatal("NewFileStore() with no bound, want an error")
	}
}

func TestAnUnconfiguredScopedStoreSaysSoRatherThanDroppingThePayload(t *testing.T) {
	t.Parallel()
	scoped := binary.For(nil, "tenant-a", "exec-1")
	if _, err := scoped.Put("a.txt", "text/plain", strings.NewReader("a")); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Put() on an unconfigured store error = %v, want it to name the missing configuration", err)
	}
	if _, _, err := scoped.Get("bin-1"); err == nil {
		t.Fatal("Get() on an unconfigured store, want an error")
	}
}

// The scope an executor runs under is fixed by the runtime, not by the node, so
// a node holding another tenant's reference still cannot resolve it.
func TestScopedBindsTheExecutionAnExecutorSees(t *testing.T) {
	t.Parallel()
	created, _ := store(t, 1024)
	reference, err := created.Put(binary.Scope{TenantID: "tenant-a", ExecutionID: "exec-1"}, "a.txt", "text/plain", strings.NewReader("a"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if _, _, err := binary.For(created, "tenant-b", "exec-1").Get(reference.ID); !errors.Is(err, binary.ErrNotFound) {
		t.Fatalf("Get() through another tenant's scope error = %v, want ErrNotFound", err)
	}
	body, _, err := binary.For(created, "tenant-a", "exec-1").Get(reference.ID)
	if err != nil {
		t.Fatalf("Get() through the owning scope error = %v", err)
	}
	body.Close()
}

func TestScopeMustNameATenantAndAnExecution(t *testing.T) {
	t.Parallel()
	created, _ := store(t, 1024)
	for name, scope := range map[string]binary.Scope{
		"no tenant":         {ExecutionID: "exec-1"},
		"no execution":      {TenantID: "tenant-a"},
		"traversing tenant": {TenantID: "../..", ExecutionID: "exec-1"},
	} {
		if _, err := created.Put(scope, "a.txt", "text/plain", strings.NewReader("a")); err == nil {
			t.Fatalf("Put() with %s, want an error", name)
		}
	}
}
