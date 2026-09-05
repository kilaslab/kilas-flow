// Package embed issues and validates the short-lived sessions that authorize
// the iframe editor.
//
// Third-party cookies are unreliable, so the host backend mints a session and
// hands the token to the iframe over postMessage. A token carries the tenant,
// the one workflow it may touch, its permissions, and the single origin
// allowed to use it.
//
// Not to be confused with internal/web, which embeds the SPA into the binary.
package embed

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Scope names one permission an embed session may carry.
type Scope string

const (
	ScopeRead  Scope = "workflow:read"
	ScopeWrite Scope = "workflow:write"
	ScopeRun   Scope = "workflow:run"
)

// ErrInvalidSession reports a token that must not be honoured.
var ErrInvalidSession = errors.New("embed session is not valid")

// MaxLifetime bounds how long a session may live.
//
// An embed token travels through a host page and sits in a browser, so it is
// deliberately short: a leaked token is only useful for minutes.
const MaxLifetime = 30 * time.Minute

// DefaultLifetime is used when a caller asks for none.
const DefaultLifetime = 15 * time.Minute

// Branding is the white-label surface.
//
// Every field is a validated value, never markup: a host supplies a name, a
// URL, and a colour, and the editor renders them into elements it controls.
// There is deliberately no way to pass CSS, HTML, or a script.
type Branding struct {
	Name    string `json:"name,omitempty"`
	LogoURL string `json:"logoUrl,omitempty"`
	// Accent is a CSS colour restricted to a hex or oklch literal.
	Accent string `json:"accent,omitempty"`
	// HideRun and HideSave hide controls. They are presentation only: the
	// server still enforces scopes, so hiding a control can never be the thing
	// that stops an action.
	HideRun  bool `json:"hideRun,omitempty"`
	HideSave bool `json:"hideSave,omitempty"`
}

var (
	accentPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|oklch\([0-9a-zA-Z%.\s/]+\)|rgb\([0-9,\s%.]+\)|[a-zA-Z]{3,20})$`)
	namePattern   = regexp.MustCompile(`^[\p{L}\p{N} .,'&()\-_]{1,60}$`)
)

// Validate rejects branding that could escape its element.
func (branding Branding) Validate() error {
	if branding.Name != "" && !namePattern.MatchString(branding.Name) {
		return fmt.Errorf("branding name may contain only letters, digits, spaces, and simple punctuation")
	}
	if branding.LogoURL != "" {
		target, err := url.Parse(branding.LogoURL)
		if err != nil {
			return fmt.Errorf("branding logo must be a URL")
		}
		// Only https: a data: or javascript: URL in an img src is the classic
		// way markup injection arrives through a "safe" string field.
		if !strings.EqualFold(target.Scheme, "https") || target.Host == "" {
			return fmt.Errorf("branding logo must be an absolute https URL")
		}
	}
	if branding.Accent != "" && !accentPattern.MatchString(branding.Accent) {
		return fmt.Errorf("branding accent must be a plain CSS colour")
	}
	return nil
}

// Session is one issued embed authorization.
type Session struct {
	ID         string    `json:"sid"`
	TenantID   string    `json:"tid"`
	WorkflowID string    `json:"wid"`
	Scopes     []Scope   `json:"scp"`
	Origin     string    `json:"org"`
	IssuedAt   time.Time `json:"iat"`
	ExpiresAt  time.Time `json:"exp"`
	Branding   Branding  `json:"brd,omitempty"`
}

// Allows reports whether the session carries a scope.
func (session Session) Allows(scope Scope) bool {
	for _, held := range session.Scopes {
		if held == scope {
			return true
		}
		// Write implies read: a session that can save must be able to load.
		if scope == ScopeRead && (held == ScopeWrite || held == ScopeRun) {
			return true
		}
	}
	return false
}

// MatchesOrigin compares the request's Origin against the session's.
//
// The comparison is exact after normalization. There is no wildcard and no
// suffix match: an embed token is minted for one host page, and "trust
// anything under this domain" is precisely the loophole a subdomain takeover
// walks through.
func (session Session) MatchesOrigin(origin string) bool {
	if session.Origin == "" {
		return false
	}
	return NormalizeOrigin(origin) == session.Origin
}

// NormalizeOrigin reduces an origin to scheme://host[:port], lowercased.
func NormalizeOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" {
		return ""
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "https" && scheme != "http" {
		return ""
	}
	return scheme + "://" + strings.ToLower(target.Host)
}

// Issuer mints and verifies embed tokens.
type Issuer struct {
	key []byte
	// allowedOrigins is the deployment's allowlist. Session creation refuses
	// any origin outside it, so a compromised host integration cannot mint a
	// token for a page the operator never approved.
	allowedOrigins []string
	now            func() time.Time
}

// NewIssuer builds the token issuer. The signing key must be at least 32 bytes.
func NewIssuer(key []byte, allowedOrigins []string, now func() time.Time) (*Issuer, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("embed signing key must be at least 32 bytes")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	normalized := make([]string, 0, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if normalized_origin := NormalizeOrigin(origin); normalized_origin != "" {
			normalized = append(normalized, normalized_origin)
		}
	}
	sort.Strings(normalized)
	return &Issuer{key: append([]byte(nil), key...), allowedOrigins: normalized, now: now}, nil
}

// AllowedOrigins returns the configured allowlist.
func (issuer *Issuer) AllowedOrigins() []string {
	return append([]string(nil), issuer.allowedOrigins...)
}

// OriginAllowed reports whether an origin may be embedded into.
func (issuer *Issuer) OriginAllowed(origin string) bool {
	normalized := NormalizeOrigin(origin)
	if normalized == "" {
		return false
	}
	// An empty allowlist means nothing is allowed. Defaulting to "everything"
	// would silently publish the editor to any site that framed it.
	for _, allowed := range issuer.allowedOrigins {
		if allowed == normalized {
			return true
		}
	}
	return false
}

// Request describes a session to mint.
type Request struct {
	TenantID   string
	WorkflowID string
	Scopes     []Scope
	Origin     string
	Lifetime   time.Duration
	Branding   Branding
	// SessionID is supplied by the caller in tests; production leaves it empty
	// and the issuer generates one.
	SessionID string
}

// Issue mints a signed token for one workflow, origin, and scope set.
func (issuer *Issuer) Issue(request Request) (Session, string, error) {
	if request.TenantID == "" || request.WorkflowID == "" {
		return Session{}, "", fmt.Errorf("an embed session needs a tenant and a workflow")
	}
	if !issuer.OriginAllowed(request.Origin) {
		return Session{}, "", fmt.Errorf("origin %q is not in this deployment's embed allowlist", request.Origin)
	}
	scopes, err := normalizeScopes(request.Scopes)
	if err != nil {
		return Session{}, "", err
	}
	if err := request.Branding.Validate(); err != nil {
		return Session{}, "", err
	}

	lifetime := request.Lifetime
	if lifetime <= 0 {
		lifetime = DefaultLifetime
	}
	if lifetime > MaxLifetime {
		lifetime = MaxLifetime
	}

	sessionID := request.SessionID
	if sessionID == "" {
		sessionID = randomID()
	}
	now := issuer.now().UTC()
	session := Session{
		ID: sessionID, TenantID: request.TenantID, WorkflowID: request.WorkflowID,
		Scopes: scopes, Origin: NormalizeOrigin(request.Origin),
		IssuedAt: now, ExpiresAt: now.Add(lifetime), Branding: request.Branding,
	}
	token, err := issuer.sign(session)
	if err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

func normalizeScopes(scopes []Scope) ([]Scope, error) {
	if len(scopes) == 0 {
		// A session with no scopes could do nothing, so an empty request is a
		// mistake rather than a read-only default.
		return nil, fmt.Errorf("an embed session needs at least one scope")
	}
	seen := map[Scope]bool{}
	normalized := make([]Scope, 0, len(scopes))
	for _, scope := range scopes {
		switch scope {
		case ScopeRead, ScopeWrite, ScopeRun:
		default:
			return nil, fmt.Errorf("scope %q is not supported", scope)
		}
		if seen[scope] {
			continue
		}
		seen[scope] = true
		normalized = append(normalized, scope)
	}
	sort.Slice(normalized, func(left, right int) bool { return normalized[left] < normalized[right] })
	return normalized, nil
}

// tokenVersion prefixes every token so the format can change without an older
// token being reinterpreted under new rules.
const tokenVersion = "kfe1"

func (issuer *Issuer) sign(session Session) (string, error) {
	payload, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("encode embed session: %w", err)
	}
	body := tokenVersion + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := issuer.mac(body)
	return body + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (issuer *Issuer) mac(body string) []byte {
	mac := hmac.New(sha256.New, issuer.key)
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

// Verify checks a token's signature, version, and expiry.
//
// The signature is checked before the payload is parsed, so a forged token is
// rejected without its contents ever being interpreted.
func (issuer *Issuer) Verify(token string) (Session, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[0] != tokenVersion {
		return Session{}, fmt.Errorf("%w: malformed token", ErrInvalidSession)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Session{}, fmt.Errorf("%w: malformed signature", ErrInvalidSession)
	}
	body := parts[0] + "." + parts[1]
	if subtle.ConstantTimeCompare(signature, issuer.mac(body)) != 1 {
		return Session{}, fmt.Errorf("%w: signature does not match", ErrInvalidSession)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Session{}, fmt.Errorf("%w: malformed payload", ErrInvalidSession)
	}
	var session Session
	if err := json.Unmarshal(payload, &session); err != nil {
		return Session{}, fmt.Errorf("%w: malformed payload", ErrInvalidSession)
	}
	if session.WorkflowID == "" || session.TenantID == "" || len(session.Scopes) == 0 {
		return Session{}, fmt.Errorf("%w: incomplete session", ErrInvalidSession)
	}
	if !issuer.now().UTC().Before(session.ExpiresAt) {
		return Session{}, fmt.Errorf("%w: session has expired", ErrInvalidSession)
	}
	return session, nil
}

func randomID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		// A session ID is an identifier, not a secret — the signature is what
		// authenticates — so a time-based fallback is acceptable rather than
		// failing the request.
		return fmt.Sprintf("es_%d", time.Now().UnixNano())
	}
	return "es_" + base64.RawURLEncoding.EncodeToString(buffer)
}
