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
// It is stateless: everything needed to verify it is in the token, so no table
// is read on the hot path. The cost of that choice is that a session cannot be
// revoked before it expires — signing out clears the cookie in the one browser
// holding it, and a copy taken from that browser beforehand keeps working until
// ExpiresAt. Shortening SessionTTL is the only lever against that; a revocation
// list would need storage this deliberately does not have.
type Session struct {
	UserID    string    `json:"sub"`
	TenantID  string    `json:"tid"`
	Email     string    `json:"eml"`
	IssuedAt  time.Time `json:"iat"`
	ExpiresAt time.Time `json:"exp"`
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
func (issuer *Issuer) IssueSession(userID, tenantID, email string) (Session, string, error) {
	if userID == "" || tenantID == "" {
		return Session{}, "", fmt.Errorf("a session needs a user and a tenant")
	}
	now := issuer.now().UTC()
	session := Session{
		UserID: userID, TenantID: tenantID, Email: email,
		IssuedAt: now, ExpiresAt: now.Add(issuer.sessionTTL),
	}
	token, err := issuer.sign(SessionVersion, session)
	if err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

// VerifySession checks a session token's signature and expiry.
func (issuer *Issuer) VerifySession(token string) (Session, error) {
	var session Session
	if err := issuer.verify(SessionVersion, token, &session); err != nil {
		return Session{}, err
	}
	if issuer.now().UTC().After(session.ExpiresAt) {
		return Session{}, ErrUnauthenticated
	}
	if session.UserID == "" || session.TenantID == "" {
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
