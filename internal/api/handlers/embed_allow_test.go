package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

func allowTestIssuer(t *testing.T) *embed.Issuer {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 41)
	}
	issuer, err := embed.NewIssuer(key, []string{"https://host.example"}, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	return issuer
}

func tenantAllowInput() *createEmbedSessionInput {
	input := &createEmbedSessionInput{}
	input.Body.WorkflowID = "wf_1"
	input.Body.Scopes = []string{"workflow:read"}
	input.Body.Origin = "https://host.example"
	input.Body.Allow = &EmbedAllow{Credentials: &EmbedAllowList{Tenant: true}}
	return input
}

// "tenant" hands the session the whole tenant's authority over one kind, so
// only a caller that holds the whole tenant's authority may declare it. The
// routing middleware already refuses the endpoint to a scoped key; this pins
// the handler's own answer, so the rule does not rest on a route table alone.
func TestOnlyATenantWideCallerMayDeclareAKindTenant(t *testing.T) {
	handler := NewEmbedSessions(allowTestIssuer(t), nil, nil)

	refused := map[string]auth.Principal{
		"a scoped key": {
			TenantID: repository.DefaultTenantID, KeyID: "key_agent", Kind: auth.KindAPIKey,
			Scopes: []embed.Scope{embed.ScopeWrite},
		},
		"a key bound to one workflow": {
			TenantID: repository.DefaultTenantID, KeyID: "key_bound", Kind: auth.KindAPIKey,
			Scopes: []embed.Scope{embed.ScopeRead}, WorkflowID: "wf_1",
		},
		"a workflow-bound principal with no scopes": {
			TenantID: repository.DefaultTenantID, KeyID: "key_odd", Kind: auth.KindAPIKey, WorkflowID: "wf_1",
		},
	}
	for name, principal := range refused {
		_, err := handler.Create(auth.WithPrincipal(context.Background(), principal), tenantAllowInput())
		var status huma.StatusError
		if !errors.As(err, &status) || status.GetStatus() != http.StatusForbidden {
			t.Errorf("%s: Create() error = %v, want 403", name, err)
		}
	}

	for name, ctx := range map[string]context.Context{
		"a tenant-wide key": tenantWideCaller(),
		"a dashboard user": auth.WithPrincipal(context.Background(), auth.Principal{
			TenantID: repository.DefaultTenantID, UserID: "user_1", Kind: auth.KindSession,
		}),
		"an installation with authentication off": context.Background(),
	} {
		created, err := handler.Create(ctx, tenantAllowInput())
		if err != nil {
			t.Fatalf("%s: Create() error = %v", name, err)
		}
		session, err := handler.issuer.Verify(created.Body.Token)
		if err != nil {
			t.Fatalf("%s: Verify() error = %v", name, err)
		}
		if !session.Confinement.TenantCredentials {
			t.Errorf("%s: confinement = %#v, want the credential kind open", name, session.Confinement)
		}
	}
}

// Declaring ids needs the store that can check them. A handler wired without
// one refuses the mint rather than minting ids nobody looked up.
func TestDeclaredIdsAreRefusedWhenTheyCannotBeChecked(t *testing.T) {
	handler := NewEmbedSessions(allowTestIssuer(t), nil, nil)
	input := tenantAllowInput()
	input.Body.Allow = &EmbedAllow{Credentials: &EmbedAllowList{IDs: []string{"cred_1"}}}
	_, err := handler.Create(tenantWideCaller(), input)
	var status huma.StatusError
	if !errors.As(err, &status) || status.GetStatus() != http.StatusServiceUnavailable {
		t.Fatalf("Create() error = %v, want 503", err)
	}
}

// The wire shape is `string[] | "tenant"`, in both directions.
func TestAnAllowListRoundTripsBothShapes(t *testing.T) {
	var allow EmbedAllow
	if err := json.Unmarshal([]byte(`{"credentials":"tenant","workflows":["wf_1","wf_2"]}`), &allow); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if allow.Credentials == nil || !allow.Credentials.Tenant || allow.Datastores != nil {
		t.Fatalf("credentials = %#v, datastores = %#v", allow.Credentials, allow.Datastores)
	}
	if allow.Workflows == nil || allow.Workflows.Tenant || len(allow.Workflows.IDs) != 2 {
		t.Fatalf("workflows = %#v", allow.Workflows)
	}
	encoded, err := json.Marshal(allow)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(encoded) != `{"credentials":"tenant","workflows":["wf_1","wf_2"]}` {
		t.Errorf("encoded = %s", encoded)
	}
	for _, bad := range []string{`"everyone"`, `7`, `{"ids":[]}`, `[1]`} {
		var list EmbedAllowList
		if err := json.Unmarshal([]byte(bad), &list); err == nil {
			t.Errorf("Unmarshal(%s) accepted it as %#v", bad, list)
		}
	}
}
