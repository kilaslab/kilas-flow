package repository_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// A restricted listing is filtered in the query, before the page and its cursor
// are built, so a cursor can only ever name a row the caller may see. Filtering
// the page afterwards — what the embed listing used to do — left the cursor
// pointing at the last row of the unfiltered page, which is a sibling's name
// and id in plain base64.
func TestARestrictedCredentialPageIsBuiltFromVisibleRowsOnly(t *testing.T) {
	_, store, _ := newCredentialFixture(t)
	tenant := repository.TenantScope{ID: "tenant-page"}
	ids := map[string]string{}
	for _, name := range []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo"} {
		created, err := store.Create(context.Background(), tenant, credentials.Record{
			Name: name, Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Key", "value": name},
		})
		if err != nil {
			t.Fatalf("Create(%s) error = %v", name, err)
		}
		ids[name] = created.ID
	}
	visible := []string{ids["Bravo"], ids["Delta"]}

	var names []string
	cursor := ""
	for page := 0; page < 10; page++ {
		result, err := store.ListPage(context.Background(), tenant, repository.CredentialFilter{
			Limit: 1, Cursor: cursor, Restricted: true, IDs: visible,
		})
		if err != nil {
			t.Fatalf("ListPage() error = %v", err)
		}
		for _, record := range result.Credentials {
			names = append(names, record.Name)
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
		decoded, _ := base64.RawURLEncoding.DecodeString(cursor)
		for _, hidden := range []string{"Alpha", "Charlie", "Echo"} {
			if strings.Contains(string(decoded), hidden) || strings.Contains(string(decoded), ids[hidden]) {
				t.Errorf("cursor %q names %s, which the listing may not see", decoded, hidden)
			}
		}
	}
	if strings.Join(names, ",") != "Bravo,Delta" {
		t.Errorf("listed %v, want exactly the visible rows in order", names)
	}

	// A restriction to nothing lists nothing, rather than reading an empty set
	// of ids as no restriction at all.
	empty, err := store.ListPage(context.Background(), tenant, repository.CredentialFilter{Restricted: true})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if len(empty.Credentials) != 0 || empty.NextCursor != "" {
		t.Errorf("page = %#v, want nothing for a restriction to no ids", empty)
	}
}
