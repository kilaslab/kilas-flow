package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// SessionVersion prefixes a browser session token.
	SessionVersion = "kfs1"
	// TicketVersion prefixes a stream ticket.
	TicketVersion = "kft1"
)

// SessionCookieName is where the dashboard's session lives.
//
// The __Host- prefix is deliberate: a browser only accepts such a cookie over
// HTTPS, from the exact host that set it, with Path=/ and no Domain, so a
// sibling subdomain that an attacker controls cannot plant a session here.
const SessionCookieName = "__Host-kilasflow_session"

// InsecureSessionCookieName is the fallback for a deployment served over plain
// HTTP, where a browser would refuse the __Host- prefix outright because that
// prefix requires Secure. Choosing it gives up the protections above.
const InsecureSessionCookieName = "kilasflow_session"

// CookieName returns where the session lives for a deployment.
func CookieName(secure bool) string {
	if secure {
		return SessionCookieName
	}
	return InsecureSessionCookieName
}

// DefaultSessionTTL is how long a dashboard login lasts when nothing says.
const DefaultSessionTTL = 12 * time.Hour

// MaxTicketTTL bounds a stream ticket's life.
//
// A ticket is spent within milliseconds of being minted — the client fetches it
// and immediately opens the EventSource — so seconds is generous. It is short
// because a ticket travels in a query string and therefore lands in every proxy
// access log on the way: a URL recovered from a log has to be worthless by the
// time anyone reads it.
const MaxTicketTTL = 30 * time.Second

// Session is a signed browser login.
//
// Verifying the token alone is stateless: the signature and the expiry are all
// that is needed, so an installation that never turns the account lookup on
// reads no table on the hot path. The cost of that is that a signature-only
// check cannot revoke anything — signing out clears the cookie in the one
// browser holding it, and a copy taken from that browser beforehand keeps
// working until ExpiresAt.
//
// UserVersion is the lever against the worst half of that: a verifier that does
// re-read the account refuses a session whose user has been disabled or whose
// password has changed, and the window it takes to notice is the verifier's own
// cache lifetime rather than the session's remaining hours. A build that skips
// the lookup gets the old behaviour back and should say so where it wires the
// middleware.
type Session struct {
	UserID   string `json:"sub"`
	TenantID string `json:"tid"`
	Email    string `json:"eml"`
	// UserVersion fingerprints the account's stored credential state at the
	// moment of signing in — the password hash and whether the account was
	// disabled. It is a digest, never the hash itself, so a session that leaks
	// discloses nothing about the password.
	//
	// It exists so that a token whose signer never re-reads a row can still be
	// revoked: a verifier that does look the account up compares this value
	// against the fingerprint of what is stored now, and refuses a token whose
	// account has since had its password changed or been disabled. A token
	// minted before this field existed carries nothing here and is refused —
	// every session in flight dies once on the deploy that introduces it,
	// which is the intended cost of closing that hole.
	UserVersion string    `json:"uvv"`
	IssuedAt    time.Time `json:"iat"`
	ExpiresAt   time.Time `json:"exp"`
}

// userVersionLength is how much of the digest a session carries, in hex
// characters.
//
// 16 characters is 64 bits: far too little to attack the hash it summarises
// (finding a password whose fingerprint matches a chosen one is a 2^64 search
// even though the digest itself is public), and short enough that a session
// cookie stays small.
const userVersionLength = 16

// UserVersion builds the fingerprint a session binds to.
//
// The caller passes the account's current stored state rather than the account
// itself, so this package never needs to know how an account is stored.
func UserVersion(passwordHash string, disabled bool) string {
	// The separator makes the two halves unambiguous: a hash format that could
	// end in the flag's own text must not fingerprint the same as a
	// passwordless account of the other state.
	state := passwordHash + "\x00"
	if disabled {
		state += "disabled"
	} else {
		state += "enabled"
	}
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:userVersionLength/2])
}

// Ticket authorises one execution's event stream for one caller.
type Ticket struct {
	ID          string    `json:"jti"`
	TenantID    string    `json:"tid"`
	ExecutionID string    `json:"xid"`
	ExpiresAt   time.Time `json:"exp"`
}

// Issuer signs and verifies sessions and stream tickets.
//
// It shares no key with internal/embed. A key that signed both would let a
// forged value of one kind be presented as the other if either payload ever
// grew a field the other's parser also accepts.
type Issuer struct {
	key        []byte
	sessionTTL time.Duration
	now        func() time.Time

	// spent records tickets that have already opened a stream. It is process
	// local: in a multi-instance deployment a ticket replayed against a
	// different instance within its few seconds of life is not caught here.
	// The short TTL, not this map, is what bounds that exposure.
	spentMu sync.Mutex
	spent   map[string]time.Time
}

// NewIssuer builds the signer. The key must be at least 32 bytes.
func NewIssuer(key []byte, sessionTTL time.Duration, now func() time.Time) (*Issuer, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("auth signing key must be at least 32 bytes")
	}
	if sessionTTL <= 0 {
		sessionTTL = DefaultSessionTTL
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Issuer{
		key:        append([]byte(nil), key...),
		sessionTTL: sessionTTL,
		now:        now,
		spent:      map[string]time.Time{},
	}, nil
}

// SessionTTL reports how long a new login lasts.
func (issuer *Issuer) SessionTTL() time.Duration { return issuer.sessionTTL }

// IssueSession mints a dashboard login for one user.
//
// userVersion is the fingerprint of the account's stored credential state, from
// UserVersion. It is required rather than optional: a session that carries no
// fingerprint can never be revalidated, so minting one would be minting a token
// that no verifier can revoke. An empty value is a caller bug, and reporting it
// here is better than issuing a session the middleware will refuse.
func (issuer *Issuer) IssueSession(userID, tenantID, email, userVersion string) (Session, string, error) {
	if userID == "" || tenantID == "" {
		return Session{}, "", fmt.Errorf("a session needs a user and a tenant")
	}
	if userVersion == "" {
		return Session{}, "", fmt.Errorf("a session needs a user version")
	}
	now := issuer.now().UTC()
	session := Session{
		UserID: userID, TenantID: tenantID, Email: email,
		UserVersion: userVersion,
		IssuedAt:    now, ExpiresAt: now.Add(issuer.sessionTTL),
	}
	token, err := issuer.sign(SessionVersion, session)
	if err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

// VerifySession checks a session token's signature, expiry, and shape.
//
// The signature is what makes the token unforgeable; the shape checks make a
// signed-but-unusable token refuse rather than authenticate. A session with no
// UserVersion is one signed before the fingerprint existed, so it is refused
// here: the middleware cannot revalidate it, and admitting it would hand every
// still-valid old cookie a pass through the revalidation it is the whole point
// of the field to enforce.
func (issuer *Issuer) VerifySession(token string) (Session, error) {
	var session Session
	if err := issuer.verify(SessionVersion, token, &session); err != nil {
		return Session{}, err
	}
	if issuer.now().UTC().After(session.ExpiresAt) {
		return Session{}, ErrUnauthenticated
	}
	if session.UserID == "" || session.TenantID == "" || session.UserVersion == "" {
		return Session{}, ErrUnauthenticated
	}
	return session, nil
}

// IssueTicket mints a single-use authorization for one execution's stream.
func (issuer *Issuer) IssueTicket(tenantID, executionID string, lifetime time.Duration) (Ticket, string, error) {
	if tenantID == "" || executionID == "" {
		return Ticket{}, "", fmt.Errorf("a stream ticket needs a tenant and an execution")
	}
	if lifetime <= 0 || lifetime > MaxTicketTTL {
		lifetime = MaxTicketTTL
	}
	id, err := randomID()
	if err != nil {
		return Ticket{}, "", err
	}
	ticket := Ticket{
		ID: id, TenantID: tenantID, ExecutionID: executionID,
		ExpiresAt: issuer.now().UTC().Add(lifetime),
	}
	token, err := issuer.sign(TicketVersion, ticket)
	if err != nil {
		return Ticket{}, "", err
	}
	return ticket, token, nil
}

// RedeemTicket verifies a stream ticket and spends it.
//
// Spending is what makes a ticket recovered from an access log useless even
// inside its lifetime: the connection it authorised has already claimed it.
func (issuer *Issuer) RedeemTicket(token, executionID string) (Ticket, error) {
	var ticket Ticket
	if err := issuer.verify(TicketVersion, token, &ticket); err != nil {
		return Ticket{}, err
	}
	now := issuer.now().UTC()
	if now.After(ticket.ExpiresAt) || ticket.TenantID == "" {
		return Ticket{}, ErrUnauthenticated
	}
	// A ticket names the one execution it opens, so a ticket for a run the
	// caller may see cannot be turned sideways onto one it may not.
	if ticket.ExecutionID != executionID {
		return Ticket{}, ErrUnauthenticated
	}
	if !issuer.claim(ticket.ID, ticket.ExpiresAt, now) {
		return Ticket{}, ErrUnauthenticated
	}
	return ticket, nil
}

// claim records a ticket as spent, reporting false if it already was.
func (issuer *Issuer) claim(id string, expiresAt, now time.Time) bool {
	issuer.spentMu.Lock()
	defer issuer.spentMu.Unlock()

	// Expired entries are dropped on the way past rather than by a sweeper
	// goroutine, so an idle process holds no timer and the map cannot grow
	// beyond the tickets issued within one TTL window.
	for spentID, spentExpiry := range issuer.spent {
		if now.After(spentExpiry) {
			delete(issuer.spent, spentID)
		}
	}
	if _, used := issuer.spent[id]; used {
		return false
	}
	issuer.spent[id] = expiresAt
	return true
}

func (issuer *Issuer) sign(version string, payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode %s token: %w", version, err)
	}
	body := version + "." + base64.RawURLEncoding.EncodeToString(encoded)
	return body + "." + base64.RawURLEncoding.EncodeToString(issuer.mac(body)), nil
}

// verify checks the signature before the payload is parsed, so a forged token's
// contents are never interpreted.
func (issuer *Issuer) verify(version, token string, into any) error {
	token = strings.TrimSpace(token)
	// Split at the last dot rather than the first, so the signature stays
	// correct if a later token version gives the payload more than one segment.
	split := strings.LastIndex(token, ".")
	if split < 0 {
		return ErrUnauthenticated
	}
	body, signature := token[:split], token[split+1:]

	presented, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return ErrUnauthenticated
	}
	if subtle.ConstantTimeCompare(presented, issuer.mac(body)) != 1 {
		return ErrUnauthenticated
	}

	prefix, payload, found := strings.Cut(body, ".")
	if !found || prefix != version {
		return ErrUnauthenticated
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return ErrUnauthenticated
	}
	if err := json.Unmarshal(decoded, into); err != nil {
		return ErrUnauthenticated
	}
	return nil
}

func (issuer *Issuer) mac(body string) []byte {
	mac := hmac.New(sha256.New, issuer.key)
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate a token identifier: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
