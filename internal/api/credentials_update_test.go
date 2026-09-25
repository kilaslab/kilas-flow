package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// An update carries what the caller changed. A field it did not send is a
// field it did not mean to touch, and clearing it is data loss the caller
// cannot see: the secret never came back to it, so nothing on its screen says
// the key is gone.
func TestAnUpdateKeepsTheSecretsItDidNotSend(t *testing.T) {
	handler, store := credentialAPI(t, api.Deps{})
	created := storeCredential(t, handler, "Verifier", "jwtAuth", map[string]string{
		"keyType": "passphrase", "secret": "hs-secret", "privateKey": "pem-private", "algorithm": "HS256",
	})

	// A rename that sends neither secret, the way a client that only knows the
	// public half would write it.
	requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name":   "Verifier (renamed)",
		"fields": map[string]string{"keyType": "passphrase", "algorithm": "HS256"},
	}, http.StatusOK)

	_, fields, err := store.Resolve(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["secret"] != "hs-secret" || fields["privateKey"] != "pem-private" {
		t.Fatalf("stored secrets after an update that omitted them = %#v, want both kept", fields)
	}

	// An explicit empty string is still how a caller clears a field.
	requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name":   "Verifier (renamed)",
		"fields": map[string]string{"keyType": "passphrase", "algorithm": "HS256", "privateKey": ""},
	}, http.StatusOK)
	_, fields, err = store.Resolve(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["privateKey"] != "" || fields["secret"] != "hs-secret" {
		t.Fatalf("stored secrets after clearing privateKey = %#v", fields)
	}
}

func TestAGoogleUpdateKeepsTheRefreshTokenConnectStored(t *testing.T) {
	handler, store := credentialAPI(t, api.Deps{})
	created := storeCredential(t, handler, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "tenant-csec",
	})
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	record, fields, err := store.Resolve(context.Background(), tenant, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// What Connect writes after the popup.
	fields["access_token"] = "ya29.access"
	fields["refresh_token"] = "1//refresh"
	if _, err := store.Update(context.Background(), tenant, created.ID, credentials.Record{
		Name: record.Name, Fields: fields, AllowedDomains: record.AllowedDomains,
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// The dashboard edits the client id and sends nothing for the tokens.
	requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name":   "Drive",
		"fields": map[string]string{"clientId": "tenant-cid-2"},
	}, http.StatusOK)
	_, fields, err = store.Resolve(context.Background(), tenant, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["clientId"] != "tenant-cid-2" {
		t.Fatalf("clientId = %q, want the edit applied", fields["clientId"])
	}
	if fields["clientSecret"] != "tenant-csec" || fields["refresh_token"] != "1//refresh" || fields["access_token"] != "ya29.access" {
		t.Fatalf("stored fields = %#v, want the client secret and both tokens kept", fields)
	}
}

// An omitted scope is not "unrestricted". Reading it that way means a client
// that never sends allowedDomains — any client that only renames — silently
// lets the secret go to every host.
func TestAnUpdateThatOmitsTheScopeKeepsIt(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	created := requestJSON[credentialResource](t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Scoped", "type": "httpBearerAuth",
		"fields":         map[string]string{"token": "keep-me"},
		"allowedDomains": []string{"api.partner.test"},
	}, http.StatusCreated)

	renamed := requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name": "Scoped (renamed)", "fields": map[string]string{},
	}, http.StatusOK)
	if len(renamed.AllowedDomains) != 1 || renamed.AllowedDomains[0] != "api.partner.test" {
		t.Fatalf("allowedDomains after an update that omitted it = %#v, want the stored scope", renamed.AllowedDomains)
	}

	// An explicit empty list is the caller saying "unrestricted", and it is
	// honoured as that.
	cleared := requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name": "Scoped (renamed)", "fields": map[string]string{}, "allowedDomains": []string{},
	}, http.StatusOK)
	if len(cleared.AllowedDomains) != 0 {
		t.Fatalf("allowedDomains after an explicit empty list = %#v, want none", cleared.AllowedDomains)
	}
}

func TestAGoogleUpdateThatOmitsTheScopeDoesNotResetItToTheDefaults(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	created := requestJSON[credentialResource](t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Drive", "type": credentials.GoogleDriveOAuthType,
		"fields":         map[string]string{"clientId": "tenant-cid", "clientSecret": "tenant-csec"},
		"allowedDomains": []string{"www.googleapis.com"},
	}, http.StatusCreated)

	renamed := requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name": "Drive (renamed)", "fields": map[string]string{},
	}, http.StatusOK)
	if len(renamed.AllowedDomains) != 1 || renamed.AllowedDomains[0] != "www.googleapis.com" {
		t.Fatalf("allowedDomains = %#v, want the narrower stored scope, not the defaults", renamed.AllowedDomains)
	}

	// An explicit empty list on a Google credential still means "the Google
	// hosts", as it does at create.
	cleared := requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name": "Drive (renamed)", "fields": map[string]string{}, "allowedDomains": []string{},
	}, http.StatusOK)
	if len(cleared.AllowedDomains) != len(credentials.GoogleDefaultDomains) {
		t.Fatalf("allowedDomains = %#v, want the Google defaults", cleared.AllowedDomains)
	}
}

// The placeholder is what the API shows in place of a secret. Stored as the
// secret itself, it authenticates every later request with eight bullets and
// is indistinguishable, on every read, from a real value.
func TestCreateRefusesTheRedactionPlaceholder(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	requestProblem(t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Copied", "type": "httpBearerAuth",
		"fields": map[string]string{"token": credentials.RedactedValue},
	}, http.StatusUnprocessableEntity)
	requestProblem(t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Copied", "type": "httpBasicAuth",
		"fields": map[string]string{"user": credentials.RedactedValue, "password": "hunter2"},
	}, http.StatusUnprocessableEntity)
}

// An update that sends the placeholder for a secret that was never set keeps
// it unset, rather than storing the bullets as its value.
func TestAPlaceholderForAnUnsetSecretIsNotStored(t *testing.T) {
	handler, store := credentialAPI(t, api.Deps{})
	created := storeCredential(t, handler, "Verifier", "jwtAuth", map[string]string{
		"keyType": "passphrase", "secret": "hs-secret", "algorithm": "HS256",
	})
	requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+created.ID, map[string]any{
		"name": "Verifier",
		"fields": map[string]string{
			"keyType": "passphrase", "algorithm": "HS256",
			"secret": credentials.RedactedValue, "privateKey": credentials.RedactedValue,
		},
	}, http.StatusOK)
	_, fields, err := store.Resolve(context.Background(), repository.TenantScope{ID: repository.DefaultTenantID}, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["secret"] != "hs-secret" {
		t.Fatalf("secret = %q, want the stored value", fields["secret"])
	}
	if fields["privateKey"] == credentials.RedactedValue {
		t.Fatal("the placeholder was stored as the private key")
	}
}

// The mask means "a value is stored here". Shown for a secret that was never
// set, it tells the editor a private key exists that does not.
func TestTheMaskShowsOnlyForSecretsThatAreSet(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	created := storeCredential(t, handler, "Verifier", "jwtAuth", map[string]string{
		"keyType": "passphrase", "secret": "hs-secret", "algorithm": "HS256",
	})
	if created.Fields["secret"] != credentials.RedactedValue {
		t.Fatalf("secret = %q, want the mask for a stored value", created.Fields["secret"])
	}
	if created.Fields["privateKey"] == credentials.RedactedValue {
		t.Fatal("privateKey shows the mask although it was never set")
	}

	fetched := requestJSON[credentialResource](t, handler, http.MethodGet, "/api/v1/credentials/"+created.ID, nil, http.StatusOK)
	if fetched.Fields["secret"] != credentials.RedactedValue || fetched.Fields["privateKey"] == credentials.RedactedValue {
		t.Fatalf("GET fields = %#v, want the mask on secret only", fetched.Fields)
	}
	// The bookkeeping that makes this possible is not a field.
	for key := range fetched.Fields {
		if key != "keyType" && key != "secret" && key != "publicKey" && key != "privateKey" && key != "algorithm" {
			t.Errorf("GET returned an undeclared field %q", key)
		}
	}
}
