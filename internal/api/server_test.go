package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/config"
)

// stubPinger stands in for the database.
type stubPinger struct{ err error }

func (s stubPinger) Ping(context.Context) error { return s.err }

func newTestServer(t *testing.T, db api.Deps) http.Handler {
	t.Helper()

	if db.Config.Server.Port == 0 {
		db.Config = config.Default()
	}
	if db.Logger == nil {
		db.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if db.Version == "" {
		db.Version = "0.0.0-test"
	}

	return api.NewServer(db).Handler()
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	return rec
}

func TestHealth(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}, Version: "1.2.3"})

	rec := get(t, h, "/api/v1/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}

	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", body.Version)
	}
}

func TestReady(t *testing.T) {
	t.Run("database up", func(t *testing.T) {
		h := newTestServer(t, api.Deps{DB: stubPinger{}})

		rec := get(t, h, "/api/v1/ready")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	// A ready probe that stays green while the database is down is worse than
	// no probe at all, so this is the assertion that matters.
	t.Run("database down returns 503", func(t *testing.T) {
		h := newTestServer(t, api.Deps{DB: stubPinger{err: errors.New("connection refused")}})

		rec := get(t, h, "/api/v1/ready")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
}

// The generated document is the API contract; if it stops describing the
// operations, the "API-first" guarantee is gone.
func TestOpenAPIDocument(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	rec := get(t, h, "/api/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Errorf("openapi = %q, want 3.1.x", doc.OpenAPI)
	}

	for _, want := range []string{"/api/v1/health", "/api/v1/ready"} {
		if _, ok := doc.Paths[want]; !ok {
			t.Errorf("path %s missing from the document", want)
		}
	}
}

func TestOpenAPIYAML(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	if rec := get(t, h, "/api/openapi.yaml"); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestDocsUI(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	rec := get(t, h, "/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()

	if !strings.Contains(body, `id="api-reference"`) {
		t.Error("docs page is missing the Scalar mount point")
	}
	if !strings.Contains(body, api.OpenAPIPath+".json") {
		t.Error("docs page does not point at the generated specification")
	}
}

// kilasflow ships as a self-contained binary and is embedded into other companies'
// products, so the docs page must not reach out to a CDN. Huma's built-in
// renderer does exactly that, which is why openAPIConfig disables it.
func TestDocsUIHasNoExternalDependencies(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	body := get(t, h, "/docs").Body.String()

	for _, host := range []string{"unpkg.com", "cdn.jsdelivr.net", "cdnjs.cloudflare.com", "https://"} {
		if strings.Contains(body, host) {
			t.Errorf("docs page references %q; it must load only same-origin assets", host)
		}
	}

	if !strings.Contains(body, api.ScalarBundlePath) {
		t.Errorf("docs page does not load the vendored bundle from %s", api.ScalarBundlePath)
	}
}

func TestWebhookPrefixIsReservedNotSPA(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	rec := get(t, h, "/webhook/abc123")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type = %q, want JSON so API clients are not handed HTML", ct)
	}
}

// Deep links must reach the SPA, otherwise refreshing /app/workflows/:id 404s.
func TestSPAFallback(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	for _, path := range []string{"/", "/app/workflows/wf_123", "/embed/wf_123"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s did not return the SPA document", path)
		}
	}
}

// The SPA route is registered last and must never shadow the API.
func TestAPIRoutesWinOverSPA(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	rec := get(t, h, "/api/v1/health")
	if strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Error("the SPA handler swallowed an API route")
	}
}

func TestRequestIDEchoed(t *testing.T) {
	h := newTestServer(t, api.Deps{DB: stubPinger{}})

	t.Run("generated when absent", func(t *testing.T) {
		rec := get(t, h, "/api/v1/health")
		if rec.Header().Get("X-Request-ID") == "" {
			t.Error("X-Request-ID not set")
		}
	})

	t.Run("inbound value preserved", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set("X-Request-ID", "trace-from-host-saas")
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("X-Request-ID"); got != "trace-from-host-saas" {
			t.Errorf("X-Request-ID = %q, want the inbound value", got)
		}
	})
}
