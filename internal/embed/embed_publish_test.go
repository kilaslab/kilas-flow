package embed_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// A white-label host opts its customers into publishing and into activation
// one scope at a time: neither is implied by write, neither implies the other,
// and both imply read, because a session that can make a revision live must be
// able to load the workflow it is making live.
func TestPublishAndActivateAreMintableAndImplyOnlyRead(t *testing.T) {
	t.Parallel()

	normalized, err := embed.NormalizeScopes([]embed.Scope{embed.ScopeWrite, embed.ScopePublish, embed.ScopeActivate, embed.ScopePublish})
	if err != nil {
		t.Fatalf("NormalizeScopes() error = %v, want publish and activate accepted", err)
	}
	want := []embed.Scope{embed.ScopeActivate, embed.ScopePublish, embed.ScopeWrite}
	if strings.Join(scopeStrings(normalized), ",") != strings.Join(scopeStrings(want), ",") {
		t.Fatalf("NormalizeScopes() = %#v, want %#v", normalized, want)
	}

	issuer := newIssuer(t, nil)
	for _, scope := range []embed.Scope{embed.ScopePublish, embed.ScopeActivate} {
		request := validRequest()
		request.Scopes = []embed.Scope{scope}
		session, token, err := issuer.Issue(request)
		if err != nil {
			t.Fatalf("Issue(%q) error = %v", scope, err)
		}
		verified, err := issuer.Verify(token)
		if err != nil {
			t.Fatalf("Verify(%q) error = %v", scope, err)
		}
		for _, held := range []embed.Session{session, verified} {
			if !held.Allows(scope) || !held.Allows(embed.ScopeRead) {
				t.Errorf("%q session = %#v, want it to hold the scope and imply read", scope, held.Scopes)
			}
			if held.Allows(embed.ScopeWrite) || held.Allows(embed.ScopeRun) {
				t.Errorf("%q session = %#v, want no write or run", scope, held.Scopes)
			}
		}
	}

	everyEditorScope := validRequest()
	everyEditorScope.Scopes = []embed.Scope{embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun}
	editor, _, err := issuer.Issue(everyEditorScope)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if editor.Allows(embed.ScopePublish) || editor.Allows(embed.ScopeActivate) {
		t.Errorf("read, write and run implied more than they hold: %#v", editor.Scopes)
	}

	publish := validRequest()
	publish.Scopes = []embed.Scope{embed.ScopePublish}
	if publisher, _, err := issuer.Issue(publish); err != nil || publisher.Allows(embed.ScopeActivate) {
		t.Errorf("publish session = %#v, %v, want it minted without activate", publisher.Scopes, err)
	}
	activate := validRequest()
	activate.Scopes = []embed.Scope{embed.ScopeActivate}
	if activator, _, err := issuer.Issue(activate); err != nil || activator.Allows(embed.ScopePublish) {
		t.Errorf("activate session = %#v, %v, want it minted without publish", activator.Scopes, err)
	}

	// Both are workflow scopes, so a datastore session cannot carry them.
	for _, scope := range []embed.Scope{embed.ScopePublish, embed.ScopeActivate} {
		datastore := validRequest()
		datastore.WorkflowID, datastore.DatastoreID = "", "datastore_1"
		datastore.Scopes = []embed.Scope{scope}
		if _, _, err := issuer.Issue(datastore); err == nil {
			t.Errorf("a datastore session carrying %q was minted", scope)
		}
	}
}

// A scoped API key keeps the vocabulary it had: publishing and activation stay
// with the tenant-wide key, so the key form refuses the two embed-only scopes
// by name rather than storing a scope its arm would never honour.
func TestAnAPIKeyCannotHoldTheEmbedOnlyScopes(t *testing.T) {
	t.Parallel()

	for _, scope := range []embed.Scope{embed.ScopePublish, embed.ScopeActivate} {
		_, err := embed.NormalizeKeyScopes([]embed.Scope{embed.ScopeRead, scope})
		if err == nil {
			t.Fatalf("NormalizeKeyScopes(%q) accepted it", scope)
		}
		if !strings.Contains(err.Error(), string(scope)) || !strings.Contains(err.Error(), "embed sessions only") {
			t.Errorf("NormalizeKeyScopes(%q) error = %q, want it to name the scope as embed-only", scope, err)
		}
	}
	normalized, err := embed.NormalizeKeyScopes([]embed.Scope{embed.ScopeRun, embed.ScopeRead})
	if err != nil || len(normalized) != 2 {
		t.Fatalf("NormalizeKeyScopes(read, run) = %#v, %v, want both kept", normalized, err)
	}
	if _, err := embed.NormalizeKeyScopes([]embed.Scope{"workflow:delete"}); err == nil {
		t.Error("NormalizeKeyScopes accepted an unknown scope")
	}
	if _, err := embed.NormalizeKeyScopes(nil); err == nil {
		t.Error("NormalizeKeyScopes accepted an empty list, which NormalizeScopes refuses")
	}
}

func scopeStrings(scopes []embed.Scope) []string {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		out = append(out, string(scope))
	}
	return out
}
