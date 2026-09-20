package webhook

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// requestWithToken builds the delivery a caller presents, or one with no
// Authorization header at all when the token is empty.
func requestWithToken(token string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "https://hook.test/webhook/route", nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func signedToken(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return token
}

func pemPublicKey(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestVerifyJWTAcceptsATokenSignedWithTheCredentialsSecret(t *testing.T) {
	fields := map[string]string{"keyType": "passphrase", "secret": "hunter2-secret", "algorithm": "HS256"}
	token := signedToken(t, jwt.SigningMethodHS256, []byte("hunter2-secret"), jwt.MapClaims{"sub": "ada"})

	claims, status, err := verifyJWT(requestWithToken(token), fields)
	if err != nil {
		t.Fatalf("verifyJWT() error = %v (status %d)", err, status)
	}
	if claims["sub"] != "ada" {
		t.Errorf("claims = %#v, want the token's subject", claims)
	}
}

func TestVerifyJWTRefusesCallersItCannotVerify(t *testing.T) {
	fields := map[string]string{"keyType": "passphrase", "secret": "hunter2-secret", "algorithm": "HS256"}
	expired := jwt.MapClaims{"sub": "ada", "exp": time.Now().Add(-time.Minute).Unix()}
	// A token whose header says "none" is the algorithm-confusion attempt: the
	// signature is absent and the header claims no verification is needed.
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"mallory"}`)) + "."

	for name, token := range map[string]string{
		"signed with another secret": signedToken(t, jwt.SigningMethodHS256, []byte("other-secret"), jwt.MapClaims{"sub": "ada"}),
		"expired":                    signedToken(t, jwt.SigningMethodHS256, []byte("hunter2-secret"), expired),
		"never signed":               unsigned,
	} {
		claims, status, err := verifyJWT(requestWithToken(token), fields)
		if err == nil {
			t.Errorf("%s: verifyJWT() accepted the token (%#v)", name, claims)
			continue
		}
		if status != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, status)
		}
	}

	if _, status, err := verifyJWT(requestWithToken(""), fields); err == nil || status != http.StatusUnauthorized {
		t.Errorf("a request with no Authorization header = (%d, %v), want 401", status, err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://hook.test/webhook/route", nil)
	request.Header.Set("Authorization", "Basic YWRhOmh1bnRlcjI=")
	if _, status, err := verifyJWT(request, fields); err == nil || status != http.StatusUnauthorized {
		t.Errorf("a basic-auth caller = (%d, %v), want 401", status, err)
	}
}

func TestVerifyJWTRefusesAnalgorithmThisServerCannotVerify(t *testing.T) {
	for name, fields := range map[string]map[string]string{
		"none":  {"keyType": "passphrase", "secret": "hunter2-secret", "algorithm": "none"},
		"empty": {"keyType": "passphrase", "secret": "hunter2-secret"},
	} {
		token := signedToken(t, jwt.SigningMethodHS256, []byte("hunter2-secret"), jwt.MapClaims{"sub": "ada"})
		if _, status, err := verifyJWT(requestWithToken(token), fields); err == nil || status != http.StatusInternalServerError {
			t.Errorf("%s algorithm = (%d, %v), want 500", name, status, err)
		}
	}

	// A credential with no secret is this endpoint's misconfiguration, not the
	// caller's failure.
	token := signedToken(t, jwt.SigningMethodHS256, []byte("hunter2-secret"), jwt.MapClaims{"sub": "ada"})
	if _, status, err := verifyJWT(requestWithToken(token), map[string]string{"algorithm": "HS256"}); err == nil || status != http.StatusInternalServerError {
		t.Errorf("a passphrase credential with no secret = (%d, %v), want 500", status, err)
	}
}

func TestVerifyJWTRefusesAKeyTypeThatContradictsItsAlgorithm(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	// A credential that declares a PEM key type while naming an HMAC algorithm
	// would verify HMAC over a public key — material the credential itself
	// returns as non-secret — so it is refused rather than honoured.
	hmacToken := signedToken(t, jwt.SigningMethodHS256, []byte("hunter2-secret"), jwt.MapClaims{"sub": "ada"})
	pemAsSecret := map[string]string{
		"keyType": "pemKey", "publicKey": pemPublicKey(t, &privateKey.PublicKey), "algorithm": "HS256",
	}
	if _, status, err := verifyJWT(requestWithToken(hmacToken), pemAsSecret); err == nil || status != http.StatusInternalServerError {
		t.Errorf("a PEM key type with HS256 = (%d, %v), want 500", status, err)
	}

	rsToken := signedToken(t, jwt.SigningMethodRS256, privateKey, jwt.MapClaims{"sub": "ada"})
	passphraseAsKey := map[string]string{
		"keyType": "passphrase", "secret": "hunter2-secret",
		"publicKey": pemPublicKey(t, &privateKey.PublicKey), "algorithm": "RS256",
	}
	if _, status, err := verifyJWT(requestWithToken(rsToken), passphraseAsKey); err == nil || status != http.StatusInternalServerError {
		t.Errorf("a passphrase key type with RS256 = (%d, %v), want 500", status, err)
	}

	// A payload that predates the field, or one that lost it, keeps working as
	// a passphrase credential — the field's own default — rather than being
	// refused for a missing value the type declares as required.
	if _, _, err := verifyJWT(requestWithToken(hmacToken), map[string]string{"secret": "hunter2-secret", "algorithm": "HS256"}); err != nil {
		t.Errorf("an HS256 credential with no key type field = %v, want it read as a passphrase", err)
	}
}

func TestVerifyJWTVerifiesRSAndPSTokensAgainstTheCredentialsPublicKey(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	fields := map[string]string{"keyType": "pemKey", "publicKey": pemPublicKey(t, &privateKey.PublicKey), "algorithm": "RS256"}
	claims := jwt.MapClaims{"sub": "ada"}
	if got, _, err := verifyJWT(requestWithToken(signedToken(t, jwt.SigningMethodRS256, privateKey, claims)), fields); err != nil {
		t.Fatalf("verifyJWT() RS256 error = %v", err)
	} else if got["sub"] != "ada" {
		t.Errorf("RS256 claims = %#v, want the token's subject", got)
	}

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	if _, status, err := verifyJWT(requestWithToken(signedToken(t, jwt.SigningMethodRS256, other, claims)), fields); err == nil || status != http.StatusUnauthorized {
		t.Errorf("a token signed by another keypair = (%d, %v), want 401", status, err)
	}

	// PSS is a signing mode over the same RSA key material, so the same
	// credential verifies it once its algorithm says so.
	pss := map[string]string{"keyType": "pemKey", "publicKey": fields["publicKey"], "algorithm": "PS256"}
	if got, _, err := verifyJWT(requestWithToken(signedToken(t, jwt.SigningMethodPS256, privateKey, claims)), pss); err != nil {
		t.Fatalf("verifyJWT() PS256 error = %v", err)
	} else if got["sub"] != "ada" {
		t.Errorf("PS256 claims = %#v, want the token's subject", got)
	}

	garbage := map[string]string{"keyType": "pemKey", "publicKey": "not a key", "algorithm": "RS256"}
	token := signedToken(t, jwt.SigningMethodRS256, privateKey, claims)
	if _, status, err := verifyJWT(requestWithToken(token), garbage); err == nil || status != http.StatusInternalServerError {
		t.Errorf("an unreadable public key = (%d, %v), want 500", status, err)
	}

	// A private key is not a public key, and accepting one would mean the
	// credential store leaked a signing key into a verifier.
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}))
	if _, status, err := verifyJWT(requestWithToken(token), map[string]string{"publicKey": privatePEM, "algorithm": "RS256"}); err == nil || status != http.StatusInternalServerError {
		t.Errorf("a private key where a public key belongs = (%d, %v), want 500", status, err)
	}
}

func TestVerifyJWTVerifiesAnESTokenAgainstTheCredentialsPublicKey(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	fields := map[string]string{"keyType": "pemKey", "publicKey": pemPublicKey(t, &privateKey.PublicKey), "algorithm": "ES256"}
	claims := jwt.MapClaims{"sub": "ada"}

	got, status, err := verifyJWT(requestWithToken(signedToken(t, jwt.SigningMethodES256, privateKey, claims)), fields)
	if err != nil {
		t.Fatalf("verifyJWT() error = %v (status %d)", err, status)
	}
	if got["sub"] != "ada" {
		t.Errorf("claims = %#v, want the token's subject", got)
	}

	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	if _, status, err := verifyJWT(requestWithToken(signedToken(t, jwt.SigningMethodES256, other, claims)), fields); err == nil || status != http.StatusUnauthorized {
		t.Errorf("a token signed by another keypair = (%d, %v), want 401", status, err)
	}
}
