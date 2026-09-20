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
	// ScopeDatastoreRead grants reads of one datastore's definition and rows.
	ScopeDatastoreRead Scope = "datastore:read"
	// ScopeDatastoreWrite grants row writes in one datastore. It implies
	// ScopeDatastoreRead and nothing else: schema changes stay outside any
	// embed session, the way workflow import and activation already are.
	ScopeDatastoreWrite Scope = "datastore:write"
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
//
// A session names exactly one subject: the workflow it may open, or the
// datastore whose rows it may touch. The other subject field stays empty, so
// a token minted before datastores existed — carrying only a workflow — still
// verifies and still resolves to its original workflow scopes.
type Session struct {
	ID         string `json:"sid"`
	TenantID   string `json:"tid"`
	WorkflowID string `json:"wid"`
	// DatastoreID is the one datastore a datastore-scoped session may touch.
	// Empty on every workflow session, including every token minted before
	// this field existed.
	DatastoreID string    `json:"did,omitempty"`
	Scopes      []Scope   `json:"scp"`
	Origin      string    `json:"org"`
	IssuedAt    time.Time `json:"iat"`
	ExpiresAt   time.Time `json:"exp"`
	Branding    Branding  `json:"brd,omitempty"`
	// Confinement is what this session's document may reference: the
	// credentials, data tables, and sub-workflows a save or a run is checked
	// against. It is minted here rather than derived per request, because a
	// document cannot be its own authority — a revision poisoned before this
	// check existed would otherwise keep authorising itself forever.
	//
	// A token minted before this field existed carries none, which reads as
	// the strictest possible confinement rather than as no confinement: such a
	// session may reference nothing it did not already have.
	Confinement Confinement `json:"cfn,omitempty"`
}

// Allows reports whether the session carries a scope.
//
// Implication stays inside one family: a write that could read across
// families would hand a datastore-only session the workflow, and a
// workflow:write session the datastore rows, with nothing erroring anywhere.
func (session Session) Allows(scope Scope) bool {
	for _, held := range session.Scopes {
		if held == scope {
			return true
		}
		// Write implies read: a session that can save must be able to load.
		if scope == ScopeRead && (held == ScopeWrite || held == ScopeRun) {
			return true
		}
		if scope == ScopeDatastoreRead && held == ScopeDatastoreWrite {
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
// The server only ever passes exactly 32: the boot path decodes the configured
// variable through KeyFromEnvironment, which rejects anything else.
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
//
// Exactly one of WorkflowID and DatastoreID names the session's subject. A
// session bound to both would be ambiguous — every check downstream reads one
// field — and a session bound to neither can do nothing, so both readings are
// refused rather than minted into a token that silently reaches nowhere.
type Request struct {
	TenantID    string
	WorkflowID  string
	DatastoreID string
	Scopes      []Scope
	Origin      string
	Lifetime    time.Duration
	Branding    Branding
	// Confinement is what the new session's document may reference. The
	// caller derives it from the revision the workflow's owner published; an
	// empty value confines the session to nothing, which is the safe reading
	// for a workflow that has never been published by a trusted caller.
	Confinement Confinement
	// SessionID is supplied by the caller in tests; production leaves it empty
	// and the issuer generates one.
	SessionID string
}

// Issue mints a signed token for one workflow or one datastore, one origin,
// and one scope set.
func (issuer *Issuer) Issue(request Request) (Session, string, error) {
	if request.TenantID == "" || (request.WorkflowID == "" && request.DatastoreID == "") {
		return Session{}, "", fmt.Errorf("an embed session needs a tenant and a workflow")
	}
	if request.WorkflowID != "" && request.DatastoreID != "" {
		return Session{}, "", fmt.Errorf("an embed session is scoped to one workflow or one datastore, not both")
	}
	if !issuer.OriginAllowed(request.Origin) {
		return Session{}, "", fmt.Errorf("origin %q is not in this deployment's embed allowlist", request.Origin)
	}
	scopes, err := normalizeScopes(request.Scopes)
	if err != nil {
		return Session{}, "", err
	}
	if err := scopesMatchSubject(scopes, request.WorkflowID != ""); err != nil {
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
		DatastoreID: request.DatastoreID,
		Scopes:      scopes, Origin: NormalizeOrigin(request.Origin),
		IssuedAt: now, ExpiresAt: now.Add(lifetime), Branding: request.Branding,
		Confinement: request.Confinement.normalized(request.WorkflowID),
	}
	token, err := issuer.sign(session)
	if err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

// scopesMatchSubject refuses a scope set that could never be used: workflow
// scopes on a datastore session and datastore scopes on a workflow session
// would mint a token that passes every check yet reaches nothing, which reads
// as success to the host that requested it.
func scopesMatchSubject(scopes []Scope, workflow bool) error {
	for _, scope := range scopes {
		datastore := scope == ScopeDatastoreRead || scope == ScopeDatastoreWrite
		if datastore == workflow {
			subject, family := "a datastore session", "datastore"
			if workflow {
				subject, family = "a workflow session", "workflow"
			}
			return fmt.Errorf("scope %q does not belong on %s: mint with a %s scope", scope, subject, family)
		}
	}
	return nil
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
		case ScopeRead, ScopeWrite, ScopeRun, ScopeDatastoreRead, ScopeDatastoreWrite:
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
	// Exactly one subject, as Issue mints: a token minted before datastores
	// existed carries only a workflow and still passes, while a payload with
	// neither — or an ambiguous both — is refused without its contents ever
	// being honoured.
	subjects := 0
	if session.WorkflowID != "" {
		subjects++
	}
	if session.DatastoreID != "" {
		subjects++
	}
	if subjects != 1 || session.TenantID == "" || len(session.Scopes) == 0 {
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
