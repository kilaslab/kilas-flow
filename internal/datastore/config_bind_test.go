package datastore

import (
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/config"
)

// The operator-facing defaults in config.Datastore repeat DefaultLimits
// rather than importing them — this package imports config for
// MaxTablePrefixLength, so the import would be a cycle. This is what keeps
// the two numbers one number.
func TestConfigDatastoreDefaultsMatchDefaultLimits(t *testing.T) {
	want := DefaultLimits()
	got := config.Default().Datastore
	if got.MaxDatastoresPerTenant != want.MaxDatastoresPerTenant {
		t.Errorf("Datastore.MaxDatastoresPerTenant = %d, want %d", got.MaxDatastoresPerTenant, want.MaxDatastoresPerTenant)
	}
	if got.MaxColumnsPerDatastore != want.MaxColumnsPerDatastore {
		t.Errorf("Datastore.MaxColumnsPerDatastore = %d, want %d", got.MaxColumnsPerDatastore, want.MaxColumnsPerDatastore)
	}
	if got.MaxRowsPerDatastore != want.MaxRowsPerDatastore {
		t.Errorf("Datastore.MaxRowsPerDatastore = %d, want %d", got.MaxRowsPerDatastore, want.MaxRowsPerDatastore)
	}
	if got.MaxValueBytes != want.MaxValueBytes {
		t.Errorf("Datastore.MaxValueBytes = %d, want %d", got.MaxValueBytes, want.MaxValueBytes)
	}
}
