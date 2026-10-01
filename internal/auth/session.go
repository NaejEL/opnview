package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// The session lifetimes.
//
// TWO BOUNDS, BOTH ENFORCED, and they answer two different questions. The idle
// window closes a session somebody walked away from; the absolute cap closes one
// that has been kept warm indefinitely. An expired session is refused, never
// renewed, on either bound.
//
// Both figures are opnview's OWN and are stated as such: no survey section and no
// requirement gives a number. They are chosen for a tool that is read for a while
// and then left, on a local network, with no lockout and no delay after repeated
// failures — the requirement asks for neither, and it does ask for
// indistinguishable failures, which is what is built.
const (
	// DefaultIdleWindow closes a session no request has touched for this long.
	DefaultIdleWindow = 30 * time.Minute
	// DefaultAbsoluteLifetime closes a session this long after it was issued,
	// however active it has been.
	DefaultAbsoluteLifetime = 12 * time.Hour
)

// The session failures. Each is a state the interface names differently.
var (
	// ErrNoSession is returned when nothing identified a session: no cookie, or a
	// cookie naming a session that does not exist.
	ErrNoSession = errors.New("auth: no session")
	// ErrSessionExpired is returned when a session exists and has passed one of
	// its two bounds. It is distinct from ErrNoSession so that the interface can
	// tell somebody their session ended rather than that they never had one.
	ErrSessionExpired = errors.New("auth: the session has expired")
)

// Lifetimes are the two session bounds. They are a value so a test can drive both
// without waiting, and so raising either is a change in one place.
type Lifetimes struct {
	// Idle is the sliding window. Every validated request moves it forward.
	Idle time.Duration
	// Absolute is the cap. It is written once, when the session is issued.
	Absolute time.Duration
}

// DefaultLifetimes are the two defaults above.
func DefaultLifetimes() Lifetimes {
	return Lifetimes{Idle: DefaultIdleWindow, Absolute: DefaultAbsoluteLifetime}
}

// SessionStore is what this package needs from storage. It is an interface so
// that auth depends on no storage engine.
type SessionStore interface {
	CreateSession(ctx context.Context, session store.Session) error
	SessionByTokenDigest(ctx context.Context, digest string) (store.Session, bool, error)
	TouchSession(ctx context.Context, digest string, now int64) error
	DeleteSession(ctx context.Context, digest string) error
	DeleteExpiredSessions(ctx context.Context, now, idleCutoff int64) error
}

// Sessions issues, validates and revokes sessions.
type Sessions struct {
	store     SessionStore
	lifetimes Lifetimes
}

// NewSessions returns a session manager.
func NewSessions(sessionStore SessionStore, lifetimes Lifetimes) *Sessions {
	if lifetimes.Idle <= 0 {
		lifetimes.Idle = DefaultIdleWindow
	}
	if lifetimes.Absolute <= 0 {
		lifetimes.Absolute = DefaultAbsoluteLifetime
	}
	return &Sessions{store: sessionStore, lifetimes: lifetimes}
}

// Lifetimes returns the two bounds in force.
func (s *Sessions) Lifetimes() Lifetimes { return s.lifetimes }

// Issued is a new session: the token the caller puts in a cookie, the CSRF token
// the caller puts in a form, and the stored row.
//
// THE TOKEN IS RETURNED ONCE AND STORED NOWHERE. What the database holds is a
// digest of it, so a copied database hands over no usable session.
type Issued struct {
	// Token is the session token. It belongs in the cookie and nowhere else.
	Token string
	// CSRFToken is the session's form token.
	CSRFToken string
	// Session is the stored row.
	Session store.Session
}

// Issue creates a session for an account.
func (s *Sessions) Issue(ctx context.Context, accountID int64, now time.Time) (Issued, error) {
	token, err := RandomToken()
	if err != nil {
		return Issued{}, err
	}
	csrfToken, err := RandomToken()
	if err != nil {
		return Issued{}, err
	}
	instant := now.UTC().Unix()
	session := store.Session{
		TokenDigest: TokenDigest(token),
		AccountID:   accountID,
		CSRFToken:   csrfToken,
		CreatedAt:   instant,
		LastSeenAt:  instant,
		ExpiresAt:   now.UTC().Add(s.lifetimes.Absolute).Unix(),
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return Issued{}, err
	}
	return Issued{Token: token, CSRFToken: csrfToken, Session: session}, nil
}

// Validate resolves a token to a live session and moves the idle window forward.
//
// AN EXPIRED SESSION IS DELETED AND REFUSED, never renewed. Both bounds are
// checked before the window is touched, so a request that arrives after the idle
// window closed cannot reopen it by arriving.
func (s *Sessions) Validate(ctx context.Context, token string, now time.Time) (store.Session, error) {
	if token == "" {
		return store.Session{}, ErrNoSession
	}
	digest := TokenDigest(token)
	session, found, err := s.store.SessionByTokenDigest(ctx, digest)
	if err != nil {
		return store.Session{}, err
	}
	if !found {
		return store.Session{}, ErrNoSession
	}
	instant := now.UTC()
	switch {
	case instant.Unix() >= session.ExpiresAt,
		instant.Sub(time.Unix(session.LastSeenAt, 0).UTC()) >= s.lifetimes.Idle:
		if err := s.store.DeleteSession(ctx, digest); err != nil {
			return store.Session{}, err
		}
		return store.Session{}, ErrSessionExpired
	}
	if err := s.store.TouchSession(ctx, digest, instant.Unix()); err != nil {
		return store.Session{}, err
	}
	session.LastSeenAt = instant.Unix()
	return session, nil
}

// Revoke deletes one session. Signing out is this, and a replay of the token
// afterwards finds no row and is refused.
func (s *Sessions) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, TokenDigest(token))
}

// CollectExpired deletes every session past either bound. It keeps the table
// bounded; a session past a bound is refused whether or not this has run.
func (s *Sessions) CollectExpired(ctx context.Context, now time.Time) error {
	instant := now.UTC()
	return s.store.DeleteExpiredSessions(ctx, instant.Unix(),
		instant.Add(-s.lifetimes.Idle).Unix())
}

// tokenBytes is how much entropy a session token, a CSRF token and the setup
// token each carry. 32 bytes is the output width of the digest they are compared
// through, and there is no reason to carry less.
const tokenBytes = 32

// RandomToken returns a fresh URL-safe token.
func RandomToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generating a token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// TokenDigest is what the database stores in place of a token.
//
// SHA-256 and not a password hash, deliberately. A token is 32 bytes of
// uniform randomness, so there is no dictionary to run against it and nothing for
// a cost parameter to slow down; what the digest buys is that the stored form is
// not the usable form.
func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
