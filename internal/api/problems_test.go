package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
)

// sendRaw sends a body exactly as written, so a test can send what no encoder
// would produce: a property the schema does not have, or a document that is
// not JSON at all.
func sendRaw(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// echoedProblem is the part of a problem document an echoed value lands in.
// Value is kept raw so an absent value can be told apart from an empty one.
type echoedProblem struct {
	Errors []struct {
		Message  string          `json:"message"`
		Location string          `json:"location"`
		Value    json.RawMessage `json:"value"`
	} `json:"errors"`
}

func decodeEchoedProblem(t *testing.T, recorder *httptest.ResponseRecorder) echoedProblem {
	t.Helper()
	var problem echoedProblem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem = %v (body: %s)", err, recorder.Body)
	}
	if len(problem.Errors) == 0 {
		t.Fatalf("the problem names no error at all: %s", recorder.Body)
	}
	return problem
}

// TestAnInvalidCredentialIsNeverEchoedIntoTheProblem posts the bodies that
// used to come straight back: huma answers a missing or an unexpected property
// with the whole object it was checking, and a body it cannot parse with the
// raw bytes, so a refused create printed the very secret it refused into a
// terminal, a CI log or an agent's transcript.
func TestAnInvalidCredentialIsNeverEchoedIntoTheProblem(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})

	for _, testCase := range []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "a secret under a property the body does not have",
			body:       `{"name":"x","type":"httpHeaderAuth","data":{"name":"X-Api-Key","value":"s3cr3t-cli-skills"}}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "a secret under a misnamed fields object",
			body:       `{"name":"x","type":"httpBearerAuth","secret":{"token":"TOPSECRET-123"}}`,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "a secret in a body that is not JSON",
			body:       `{"name":"x","type":"httpBearerAuth","fields":{"token":"TOPSECRET-123"}`,
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := sendRaw(t, handler, http.MethodPost, "/api/v1/credentials", testCase.body)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", recorder.Code, testCase.wantStatus, recorder.Body)
			}
			// The whole body, not just the field a decoder would reach: a leak
			// that landed anywhere else in the response would still be a leak.
			for _, secret := range []string{"s3cr3t", "TOPSECRET"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("the problem echoed the secret: %s", recorder.Body)
				}
			}
			// Only the value goes: the caller is still told what was wrong and
			// where, which is what a problem document is for.
			for _, issue := range decodeEchoedProblem(t, recorder).Errors {
				if issue.Message == "" || issue.Location == "" {
					t.Errorf("an error lost its message or location: %+v", issue)
				}
			}
		})
	}
}

// TestASecretBearingOperationEchoesNoValueAtAll covers what the shape rule
// cannot: a secret sent where the schema wants some other scalar comes back as
// that one field's value, and on these operations any field may be a secret.
func TestASecretBearingOperationEchoesNoValueAtAll(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})

	const secret = "TOPSECRET-123"
	// Past every maxLength these operations declare, so the secret is refused
	// as itself: 1024 for a password, 255 for a key's label.
	long := secret + strings.Repeat("x", 1024)
	credential := `{"name":"x","type":"httpBearerAuth","fields":{"token":"t"},"allowedDomains":"` + secret + `"}`

	for _, testCase := range []struct {
		operation string
		method    string
		path      string
		body      string
	}{
		{"create-credential", http.MethodPost, "/api/v1/credentials", credential},
		{"update-credential", http.MethodPut, "/api/v1/credentials/cred_1", credential},
		{"test-credential-payload", http.MethodPost, "/api/v1/credential-types/httpBearerAuth/test",
			`{"fields":{"token":"t"},"allowedDomains":"` + secret + `"}`},
		{"login", http.MethodPost, "/api/v1/auth/login",
			`{"email":"ada@example.test","password":"` + long + `"}`},
		{"create-tenant-user", http.MethodPost, "/api/v1/tenants/acme/users",
			`{"email":"ada@example.test","name":"Ada","password":"` + long + `"}`},
		{"set-tenant-user-password", http.MethodPost, "/api/v1/tenants/acme/users/usr_1/password",
			`{"password":"` + long + `"}`},
		{"create-api-key", http.MethodPost, "/api/v1/api-keys", `{"label":"` + long + `"}`},
		{"create-tenant-api-key", http.MethodPost, "/api/v1/tenants/acme/api-keys", `{"label":"` + long + `"}`},
	} {
		t.Run(testCase.operation, func(t *testing.T) {
			recorder := sendRaw(t, handler, testCase.method, testCase.path, testCase.body)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
			}
			if strings.Contains(recorder.Body.String(), secret) {
				t.Fatalf("the problem echoed the secret: %s", recorder.Body)
			}
			for _, issue := range decodeEchoedProblem(t, recorder).Errors {
				if len(issue.Value) != 0 {
					t.Errorf("a secret-bearing operation echoed a value at %s: %s", issue.Location, issue.Value)
				}
			}
		})
	}
}

// TestAProblemKeepsAFieldsValueButNeverTheWholeBody pins the other half of the
// rule on an operation that carries no secret: the one value that was wrong
// still comes back, because it is the caller's own field and naming it is what
// makes the refusal actionable, while the whole object never does.
func TestAProblemKeepsAFieldsValueButNeverTheWholeBody(t *testing.T) {
	handler := newTestServer(t, api.Deps{})

	recorder := sendRaw(t, handler, http.MethodPost, "/api/v1/tenants",
		`{"id":"Not An ID","name":"Acme","token":"TOPSECRET-123"}`)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), "TOPSECRET") {
		t.Fatalf("the problem echoed the whole body: %s", recorder.Body)
	}
	kept := false
	for _, issue := range decodeEchoedProblem(t, recorder).Errors {
		if issue.Location == "body.id" && string(issue.Value) == `"Not An ID"` {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("the refused field's own value was dropped too: %s", recorder.Body)
	}
}
