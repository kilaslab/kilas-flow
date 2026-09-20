package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/embed"
)

func TestEmbedSessionCreationUsesTheConfiguredDefaultLifetime(t *testing.T) {
	fixed := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 13)
	}
	issuer, err := embed.NewIssuer(key, []string{hostOrigin}, func() time.Time { return fixed },
		embed.WithDefaultLifetime(7*time.Minute))
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	server := newTestServer(t, api.Deps{DB: stubPinger{}, EmbedIssuer: issuer})

	tests := []struct {
		name string
		ask  map[string]any
		want time.Time
	}{
		{"no ttlSeconds takes the configured default", nil, fixed.Add(7 * time.Minute)},
		{"ttlSeconds still wins", map[string]any{"ttlSeconds": 600}, fixed.Add(10 * time.Minute)},
		{"the hard cap holds over the default", map[string]any{"ttlSeconds": 86400}, fixed.Add(embed.MaxLifetime)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := map[string]any{
				"workflowId": "wf-1", "scopes": []string{"workflow:read"}, "origin": hostOrigin,
			}
			for name, value := range test.ask {
				payload[name] = value
			}
			body, _ := json.Marshal(payload)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/embed-sessions", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
			}
			var minted struct {
				ExpiresAt time.Time `json:"expiresAt"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &minted); err != nil {
				t.Fatalf("decode = %v", err)
			}
			if !minted.ExpiresAt.Equal(test.want) {
				t.Errorf("expiresAt = %s, want %s", minted.ExpiresAt, test.want)
			}
		})
	}
}

// TestEmbedSessionCreationCarriesTheDeploymentBranding covers the mint
// response a host forwards to its page: the deployment's branding.name and
// branding.logo must reach it, and a session that names its own must win field
// by field without losing the deployment's other value.
func TestEmbedSessionCreationCarriesTheDeploymentBranding(t *testing.T) {
	deployment := embed.Branding{Name: "Acme Flows", LogoURL: "https://cdn.example/logo.png"}
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 17)
	}
	issuer, err := embed.NewIssuer(key, []string{hostOrigin}, nil, embed.WithDefaultBranding(deployment))
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	server := newTestServer(t, api.Deps{DB: stubPinger{}, EmbedIssuer: issuer})

	tests := []struct {
		name string
		ask  map[string]any
		want embed.Branding
	}{
		{"a session with no branding takes the deployment defaults", nil, deployment},
		{"a session's own name wins and the default logo stays", map[string]any{"name": "Birch"},
			embed.Branding{Name: "Birch", LogoURL: deployment.LogoURL}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := map[string]any{
				"workflowId": "wf-1", "scopes": []string{"workflow:read"}, "origin": hostOrigin,
			}
			if test.ask != nil {
				payload["branding"] = test.ask
			}
			body, _ := json.Marshal(payload)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/embed-sessions", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
			}
			var minted struct {
				Branding embed.Branding `json:"branding"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &minted); err != nil {
				t.Fatalf("decode = %v", err)
			}
			if minted.Branding != test.want {
				t.Errorf("branding = %#v, want %#v", minted.Branding, test.want)
			}
		})
	}
}

// TestEmbedSessionCreationKeepsAnEmptyBrandingObjectWithoutDefaults pins the
// response shape the SDK and the reference host already read: a server with no
// deployment branding still returns an object, so a host may destructure it
// without a guard.
func TestEmbedSessionCreationKeepsAnEmptyBrandingObjectWithoutDefaults(t *testing.T) {
	server := newTestServer(t, api.Deps{DB: stubPinger{}, EmbedIssuer: embedIssuer(t)})

	body, _ := json.Marshal(map[string]any{
		"workflowId": "wf-1", "scopes": []string{"workflow:read"}, "origin": hostOrigin,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/embed-sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
	}
	var minted struct {
		Branding json.RawMessage `json:"branding"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &minted); err != nil {
		t.Fatalf("decode = %v", err)
	}
	if got := strings.TrimSpace(string(minted.Branding)); got != "{}" {
		t.Errorf("branding = %q, want an empty object", got)
	}
}
