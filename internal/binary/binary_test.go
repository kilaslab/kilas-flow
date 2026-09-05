package binary_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/binary"
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
