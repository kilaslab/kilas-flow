package credentials_test

import (
	"bytes"
	"errors"
	"github.com/kilaslab/kilas-flow/internal/property"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

func testKey() []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index)
	}
	return key
}

func TestCipherRoundTripsAPayload(t *testing.T) {
	t.Parallel()

	cipher, err := credentials.NewCipher(testKey())
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	payload := map[string]string{"user": "ada", "password": "hunter2"}

	sealed, err := cipher.Encrypt(payload)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatalf("ciphertext contains the plaintext secret: %q", sealed)
	}

	opened, err := cipher.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if opened["password"] != "hunter2" || opened["user"] != "ada" {
		t.Fatalf("Decrypt() = %#v, want the original payload", opened)
	}
}

func TestCipherProducesADifferentCiphertextEachTime(t *testing.T) {
	t.Parallel()

	cipher, _ := credentials.NewCipher(testKey())
	payload := map[string]string{"token": "same"}

	first, _ := cipher.Encrypt(payload)
	second, _ := cipher.Encrypt(payload)

	// A fresh nonce per seal keeps equal secrets from being visibly equal in
	// the database.
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of the same payload produced identical ciphertext")
	}
}

func TestCipherRejectsATamperedOrForeignCiphertext(t *testing.T) {
	t.Parallel()

	cipher, _ := credentials.NewCipher(testKey())
	sealed, _ := cipher.Encrypt(map[string]string{"token": "value"})

	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := cipher.Decrypt(tampered); err == nil {
		t.Error("a tampered ciphertext decrypted successfully")
	}

	otherKey := testKey()
	otherKey[0] ^= 0xff
	other, _ := credentials.NewCipher(otherKey)
	if _, err := other.Decrypt(sealed); err == nil {
		t.Error("a ciphertext decrypted under the wrong key")
	}

	if _, err := cipher.Decrypt([]byte("too short")); err == nil {
		t.Error("a truncated ciphertext decrypted successfully")
	}
}

func TestNewCipherRequiresA256BitKey(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 16, 31, 33} {
		if _, err := credentials.NewCipher(make([]byte, size)); err == nil {
			t.Errorf("NewCipher() accepted a %d-byte key", size)
		}
	}
}

func TestTypesDescribeTheirFieldsForAGenericEditor(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"httpBasicAuth", "httpHeaderAuth", "httpBearerAuth"} {
		definition, found := credentials.Lookup(id)
		if !found {
			t.Fatalf("credential type %q is not registered", id)
		}
		if definition.DisplayName == "" || len(definition.Fields) == 0 {
			t.Errorf("credential type %q has no editor metadata", id)
		}
		hasSecret := false
		for _, field := range definition.Fields {
			if field.Key == "" || field.Label == "" {
				t.Errorf("credential type %q has an unlabelled field", id)
			}
			hasSecret = hasSecret || field.Secret
		}
		if !hasSecret {
			t.Errorf("credential type %q declares no secret field", id)
		}
	}
	if _, found := credentials.Lookup("nope"); found {
		t.Error("an unknown credential type resolved")
	}
}

func TestValidateRejectsMissingRequiredFieldsAndUnknownKeys(t *testing.T) {
	t.Parallel()

	if err := credentials.Validate("httpBasicAuth", map[string]string{"user": "ada"}); err == nil {
		t.Error("a basic-auth credential without a password was accepted")
	}
	if err := credentials.Validate("httpBasicAuth", map[string]string{"user": "ada", "password": "x", "extra": "y"}); err == nil {
		t.Error("an unknown field was accepted")
	}
	if err := credentials.Validate("nope", map[string]string{}); err == nil {
		t.Error("an unknown credential type was accepted")
	}
	if err := credentials.Validate("httpBasicAuth", map[string]string{"user": "ada", "password": "x"}); err != nil {
		t.Errorf("a complete basic-auth credential was rejected: %v", err)
	}
}

func TestApplyAttachesAuthenticationToARequest(t *testing.T) {
	t.Parallel()

	basic, _ := http.NewRequest(http.MethodGet, "https://api.test/", nil)
	if err := credentials.Apply(basic, "httpBasicAuth", map[string]string{"user": "ada", "password": "hunter2"}); err != nil {
		t.Fatalf("Apply() basic error = %v", err)
	}
	user, password, ok := basic.BasicAuth()
	if !ok || user != "ada" || password != "hunter2" {
		t.Errorf("basic auth = (%q, %q, %v), want the credential", user, password, ok)
	}

	bearer, _ := http.NewRequest(http.MethodGet, "https://api.test/", nil)
	if err := credentials.Apply(bearer, "httpBearerAuth", map[string]string{"token": "abc123"}); err != nil {
		t.Fatalf("Apply() bearer error = %v", err)
	}
	if got := bearer.Header.Get("Authorization"); got != "Bearer abc123" {
		t.Errorf("bearer header = %q, want %q", got, "Bearer abc123")
	}

	header, _ := http.NewRequest(http.MethodGet, "https://api.test/", nil)
	if err := credentials.Apply(header, "httpHeaderAuth", map[string]string{"name": "X-Api-Key", "value": "k-1"}); err != nil {
		t.Fatalf("Apply() header error = %v", err)
	}
	if got := header.Header.Get("X-Api-Key"); got != "k-1" {
		t.Errorf("custom header = %q, want %q", got, "k-1")
	}
}

func TestQueryAuthPlacesItsParameter(t *testing.T) {
	t.Parallel()

	request, _ := http.NewRequest(http.MethodGet, "https://api.test/x?a=1", nil)
	if err := credentials.Apply(request, "httpQueryAuth", map[string]string{"name": "api_key", "value": "k-1"}); err != nil {
		t.Fatalf("Apply() query error = %v", err)
	}
	if got := request.URL.RawQuery; got != "a=1&api_key=k-1" {
		t.Errorf("query = %q, want %q", got, "a=1&api_key=k-1")
	}
}

func TestCustomAuthAppliesItsTemplate(t *testing.T) {
	t.Parallel()

	request, _ := http.NewRequest(http.MethodGet, "https://api.test/x", nil)
	template := `{"headers":{"X-Api-Key":"k-2"},"qs":{"tenant":"acme"}}`
	if err := credentials.Apply(request, "httpCustomAuth", map[string]string{"json": template}); err != nil {
		t.Fatalf("Apply() custom error = %v", err)
	}
	if got := request.Header.Get("X-Api-Key"); got != "k-2" {
		t.Errorf("custom header = %q, want %q", got, "k-2")
	}
	if got := request.URL.Query().Get("tenant"); got != "acme" {
		t.Errorf("custom query = %q, want %q", got, "acme")
	}

	empty, _ := http.NewRequest(http.MethodGet, "https://api.test/x", nil)
	if err := credentials.Apply(empty, "httpCustomAuth", map[string]string{"json": `{"headers":{}}`}); err == nil {
		t.Error("a template with neither headers nor qs was accepted")
	}

	unsupported, _ := http.NewRequest(http.MethodGet, "https://api.test/x", nil)
	err := credentials.Apply(unsupported, "httpCustomAuth", map[string]string{"json": `{"body":{"x":"1"}}`})
	if err == nil || !strings.Contains(err.Error(), "body") {
		t.Errorf("a template naming an unsupported key = %v, want an error naming body", err)
	}

	// A header name is a token, and one template can add several headers to a
	// request whose URL the node author chose — so an empty name, padding, or a
	// line break inside the name is refused rather than sent.
	for name, template := range map[string]string{
		"empty":        `{"headers":{"":"x"}}`,
		"padded":       `{"headers":{" X-Api-Key":"x"}}`,
		"line break":   `{"headers":{"X-Api-Key\r\nX-Injected":"x"}}`,
		"space inside": `{"headers":{"X Api-Key":"x"}}`,
		"colon inside": `{"headers":{"X-Api-Key:":"x"}}`,
	} {
		request, _ := http.NewRequest(http.MethodGet, "https://api.test/x", nil)
		if err := credentials.Apply(request, "httpCustomAuth", map[string]string{"json": template}); err == nil {
			t.Errorf("%s: the header name in %s was accepted", name, template)
		}
	}
}

func TestAllowsHostScopesACredentialToItsDomains(t *testing.T) {
	t.Parallel()

	record := credentials.Record{AllowedDomains: []string{"api.test", "*.internal.test"}}

	for host, want := range map[string]bool{
		"api.test":               true,
		"API.TEST":               true,
		"api.test:443":           true,
		"svc.internal.test":      true,
		"deep.svc.internal.test": true,
		"internal.test":          false,
		"evil.test":              false,
		"api.test.evil.test":     false,
	} {
		if got := record.AllowsHost(host); got != want {
			t.Errorf("AllowsHost(%q) = %v, want %v", host, got, want)
		}
	}

	// No declared scope means the credential is unrestricted, which stays an
	// explicit decision the API surfaces rather than a silent default.
	unrestricted := credentials.Record{}
	if !unrestricted.AllowsHost("anything.test") {
		t.Error("a credential with no declared domains rejected a host")
	}
}

func TestRedactedRemovesEverySecretFieldValue(t *testing.T) {
	t.Parallel()

	fields := credentials.Redacted("httpBasicAuth", map[string]string{"user": "ada", "password": "hunter2"})

	if fields["user"] != "ada" {
		t.Errorf("non-secret field = %q, want it preserved", fields["user"])
	}
	if strings.Contains(fields["password"], "hunter2") {
		t.Errorf("secret field leaked: %q", fields["password"])
	}
	if fields["password"] == "" {
		t.Error("a set secret must still be reported as set")
	}
}

// Not parallel: t.Setenv mutates process state.
func TestKeyFromEnvironmentAcceptsBase64AndHex(t *testing.T) {
	t.Setenv("KF_TEST_KEY_B64", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	key, err := credentials.KeyFromEnvironment("KF_TEST_KEY_B64")
	if err != nil || len(key) != 32 {
		t.Fatalf("KeyFromEnvironment(base64) = (%d bytes, %v), want 32 bytes", len(key), err)
	}

	t.Setenv("KF_TEST_KEY_HEX", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	hexKey, err := credentials.KeyFromEnvironment("KF_TEST_KEY_HEX")
	if err != nil || !bytes.Equal(hexKey, key) {
		t.Fatalf("KeyFromEnvironment(hex) = (%x, %v), want the same key", hexKey, err)
	}

	t.Setenv("KF_TEST_KEY_SHORT", "dG9vLXNob3J0")
	if _, err := credentials.KeyFromEnvironment("KF_TEST_KEY_SHORT"); err == nil {
		t.Error("a short key was accepted")
	}

	if _, err := credentials.KeyFromEnvironment("KF_TEST_KEY_ABSENT"); !errors.Is(err, credentials.ErrNoKey) {
		t.Errorf("missing key error = %v, want ErrNoKey", err)
	}
}

// TestEveryStoredCredentialTypeStillResolves is the migration guarantee.
//
// A stored credential row is keyed by its type ID and its sealed payload by its
// field keys. Changing either — even by tidying a name — makes an existing
// credential unresolvable with no way to recover it, so every ID and every
// field key is pinned here as a literal rather than derived from the registry,
// which would assert nothing.
func TestEveryStoredCredentialTypeStillResolves(t *testing.T) {
	t.Parallel()

	want := map[string][]string{
		"httpBasicAuth":  {"user", "password"},
		"httpHeaderAuth": {"name", "value"},
		"httpBearerAuth": {"token"},
		"jwtAuth":        {"keyType", "secret", "publicKey", "privateKey", "algorithm"},
		"httpQueryAuth":  {"name", "value"},
		"httpCustomAuth": {"json"},
		"postgres":       {"host", "port", "database", "user", "password", "sslMode"},
		"mysql":          {"host", "port", "database", "user", "password", "tls"},
		"sqlite":         {"path"},
		"wahaApi":        {"baseUrl", "apiKey"},
		"telegramApi":    {"accessToken", "baseUrl"},
		"openAiApi":      {"apiKey"},
		"openRouterApi":  {"apiKey"},
	}

	registry := credentials.NewRegistry()
	if err := credentials.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if got := len(registry.List()); got != len(want) {
		t.Errorf("registry holds %d types, want %d", got, len(want))
	}

	for id, keys := range want {
		credentialType, found := registry.Get(id)
		if !found {
			t.Errorf("credential type %q is no longer registered; every stored row of that type is unresolvable", id)
			continue
		}
		got := make([]string, 0, len(credentialType.Properties))
		for _, field := range credentialType.Properties {
			got = append(got, field.Key)
		}
		if len(got) != len(keys) {
			t.Errorf("%s field keys = %v, want %v", id, got, keys)
			continue
		}
		for index, key := range keys {
			if got[index] != key {
				t.Errorf("%s field %d = %q, want %q", id, index, got[index], key)
			}
		}
		// And a stored payload of that shape round-trips through the same
		// validation it was written under.
		payload := map[string]string{}
		for _, key := range keys {
			payload[key] = "value-" + key
		}
		if err := credentials.Validate(id, payload); err != nil {
			t.Errorf("a stored %s payload no longer validates: %v", id, err)
		}
	}
}

// TestAuthenticationIsDescribedNotSwitched is the point of the descriptor: a
// new credential type needs no Go change.
func TestAuthenticationIsDescribedNotSwitched(t *testing.T) {
	t.Parallel()

	registry := credentials.NewRegistry()
	if err := credentials.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	// A brand-new type, registered here with no change to the package. It is
	// deliberately not one this server ships: the claim under test is that a
	// type nobody wrote Go for still authenticates.
	if err := registry.Register(credentials.Type{
		ID: "exampleFixedHeaderApi", DisplayName: "Example fixed-header API",
		Properties: []property.PropertyDefinition{
			{Key: "baseUrl", Label: "Base URL", Kind: property.KindString, Required: true},
			{Key: "apiKey", Label: "API key", Kind: property.KindString, Required: true,
				TypeOptions: &property.TypeOptions{Password: true}},
		},
		Secrets:      []string{"apiKey"},
		Authenticate: &credentials.Authentication{Placement: credentials.PlacementHeader, Name: "X-Example-Key", Value: "{{ apiKey }}"},
	}); err != nil {
		t.Fatalf("Register(exampleFixedHeaderApi) error = %v", err)
	}

	for name, testCase := range map[string]struct {
		id     string
		fields map[string]string
		check  func(*testing.T, *http.Request)
	}{
		"basic": {
			id: "httpBasicAuth", fields: map[string]string{"user": "ada", "password": "secret"},
			check: func(t *testing.T, request *http.Request) {
				user, password, ok := request.BasicAuth()
				if !ok || user != "ada" || password != "secret" {
					t.Errorf("basic auth = %q/%q (ok %t)", user, password, ok)
				}
			},
		},
		"bearer": {
			id: "httpBearerAuth", fields: map[string]string{"token": "t-123"},
			check: func(t *testing.T, request *http.Request) {
				if got := request.Header.Get("Authorization"); got != "Bearer t-123" {
					t.Errorf("Authorization = %q", got)
				}
			},
		},
		"named header": {
			id: "httpHeaderAuth", fields: map[string]string{"name": "X-Api-Key", "value": "k-1"},
			check: func(t *testing.T, request *http.Request) {
				if got := request.Header.Get("X-Api-Key"); got != "k-1" {
					t.Errorf("X-Api-Key = %q", got)
				}
			},
		},
		"a type added with no Go change": {
			id: "exampleFixedHeaderApi", fields: map[string]string{"baseUrl": "https://api.test", "apiKey": "k-1"},
			check: func(t *testing.T, request *http.Request) {
				if got := request.Header.Get("X-Example-Key"); got != "k-1" {
					t.Errorf("X-Example-Key = %q, want the key applied", got)
				}
			},
		},
		// The shipped WAHA type is the same shape with the header name pinned,
		// which is what lets a generated pack authenticate with no Go at all.
		"the shipped WAHA type": {
			id: "wahaApi", fields: map[string]string{"baseUrl": "https://waha.test", "apiKey": "k-waha"},
			check: func(t *testing.T, request *http.Request) {
				if got := request.Header.Get("X-Api-Key"); got != "k-waha" {
					t.Errorf("X-Api-Key = %q, want the WAHA key", got)
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			credentialType, _ := registry.Get(testCase.id)
			request := httptest.NewRequest(http.MethodGet, "https://example.test/x", nil)
			if err := credentials.ApplyAuthentication(request, credentialType, testCase.fields); err != nil {
				t.Fatalf("ApplyAuthentication() error = %v", err)
			}
			testCase.check(t, request)
		})
	}
}

// TestADatabaseCredentialRefusesToSignAnHTTPRequest covers refusal by absence.
// Saying "this type cannot authenticate an HTTP request" is a better error than
// a default branch reached by accident.
func TestADatabaseCredentialRefusesToSignAnHTTPRequest(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"postgres", "mysql", "sqlite"} {
		credentialType, _ := credentials.Default().Get(id)
		request := httptest.NewRequest(http.MethodGet, "https://example.test/x", nil)
		err := credentials.ApplyAuthentication(request, credentialType, map[string]string{"password": "p"})
		if err == nil {
			t.Errorf("%s signed an HTTP request", id)
			continue
		}
		if !strings.Contains(err.Error(), id) {
			t.Errorf("%s error = %v, want it to name the type", id, err)
		}
	}
}

// TestRegistryRefusesDuplicatesAndUnknownSecrets keeps the registry honest.
func TestRegistryRefusesDuplicatesAndUnknownSecrets(t *testing.T) {
	t.Parallel()

	registry := credentials.NewRegistry()
	base := credentials.Type{
		ID: "test.type", DisplayName: "Test",
		Properties: []property.PropertyDefinition{
			{Key: "token", Label: "Token", Kind: property.KindString},
		},
	}
	if err := registry.Register(base); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Register(base); err == nil {
		t.Error("a duplicate credential type ID was accepted")
	}

	colliding := credentials.Type{
		ID: "test.collide", DisplayName: "Collide",
		Properties: []property.PropertyDefinition{
			{Key: "token", Label: "Token", Kind: property.KindString},
			{Key: "token", Label: "Token again", Kind: property.KindString},
		},
	}
	err := registry.Register(colliding)
	if err == nil {
		t.Error("colliding field keys were accepted")
	} else if !strings.Contains(err.Error(), "token") {
		t.Errorf("error = %v, want the offending key named", err)
	}

	if err := registry.Register(credentials.Type{
		ID: "test.badsecret", DisplayName: "Bad secret",
		Properties: []property.PropertyDefinition{{Key: "a", Label: "A", Kind: property.KindString}},
		Secrets:    []string{"nonexistent"},
	}); err == nil {
		t.Error("a secret naming an unknown field was accepted")
	}
}

// TestMaskingAndNonDisclosureAreSeparateFlags is the trap the ticket names.
//
// A field masked in the UI is not the same as a field the API never returns.
// Conflating them would make a masked-but-readable field come back as the
// placeholder, and users would overwrite real values with it.
func TestMaskingAndNonDisclosureAreSeparateFlags(t *testing.T) {
	t.Parallel()

	credentialType := credentials.Type{
		ID: "test.flags", DisplayName: "Flags",
		Properties: []property.PropertyDefinition{
			// Masked in the UI but readable back.
			{Key: "pin", Label: "PIN", Kind: property.KindString,
				TypeOptions: &property.TypeOptions{Password: true}},
			// Masked and never returned.
			{Key: "token", Label: "Token", Kind: property.KindString,
				TypeOptions: &property.TypeOptions{Password: true}},
		},
		Secrets: []string{"token"},
	}

	fields := credentialType.Fields()
	byKey := map[string]bool{}
	for _, field := range fields {
		byKey[field.Key] = field.Secret
	}
	if byKey["pin"] {
		t.Error("a password-masked field was marked non-disclosable; its real value must still be readable")
	}
	if !byKey["token"] {
		t.Error("a declared secret was not marked non-disclosable")
	}
}
