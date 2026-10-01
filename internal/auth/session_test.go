package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

// memorySessions is a session store in a map.
//
// IT IS HERE RATHER THAN A DATABASE because what these tests are about is the two
// bounds and the digest, and neither is a fact about SQLite. The bounds are asserted
// through the real store as well, from internal/web, against a real database and a real
// HTTP surface.
type memorySessions struct {
	rows map[string]store.Session
}

func newMemorySessions() *memorySessions {
	return &memorySessions{rows: map[string]store.Session{}}
}

func (m *memorySessions) CreateSession(_ context.Context, session store.Session) error {
	m.rows[session.TokenDigest] = session
	return nil
}

func (m *memorySessions) SessionByTokenDigest(_ context.Context, digest string) (store.Session, bool, error) {
	session, present := m.rows[digest]
	return session, present, nil
}

func (m *memorySessions) TouchSession(_ context.Context, digest string, now int64) error {
	session, present := m.rows[digest]
	if !present {
		return nil
	}
	session.LastSeenAt = now
	m.rows[digest] = session
	return nil
}

func (m *memorySessions) DeleteSession(_ context.Context, digest string) error {
	delete(m.rows, digest)
	return nil
}

func (m *memorySessions) DeleteExpiredSessions(_ context.Context, now, idleCutoff int64) error {
	for digest, session := range m.rows {
		if session.ExpiresAt <= now || session.LastSeenAt <= idleCutoff {
			delete(m.rows, digest)
		}
	}
	return nil
}

// TestTheTokenIsNeverStoredOnlyItsDigest.
func TestTheTokenIsNeverStoredOnlyItsDigest(t *testing.T) {
	rows := newMemorySessions()
	sessions := NewSessions(rows, DefaultLifetimes())
	now := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	issued, err := sessions.Issue(context.Background(), 1, now)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	if issued.Token == "" {
		t.Fatal("no token was issued")
	}
	for digest, session := range rows.rows {
		if digest == issued.Token || session.TokenDigest == issued.Token {
			t.Fatal("the token itself was stored")
		}
	}
	if _, present := rows.rows[TokenDigest(issued.Token)]; !present {
		t.Error("the digest of the token was not stored, so nothing can find the session")
	}
	if issued.CSRFToken == "" || issued.CSRFToken == issued.Token {
		t.Error("the form token is missing or is the session token")
	}
}

// TestASessionExpiresOnTheSlidingIdleWindow.
func TestASessionExpiresOnTheSlidingIdleWindow(t *testing.T) {
	rows := newMemorySessions()
	lifetimes := Lifetimes{Idle: 10 * time.Minute, Absolute: 100 * time.Hour}
	sessions := NewSessions(rows, lifetimes)
	now := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	issued, err := sessions.Issue(context.Background(), 1, now)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	// The window slides: repeated requests just inside it keep the session alive for
	// much longer than one window.
	for step := range 10 {
		at := now.Add(time.Duration(step+1) * 9 * time.Minute)
		if _, err := sessions.Validate(context.Background(), issued.Token, at); err != nil {
			t.Fatalf("a session touched every nine minutes was refused at step %d: %v", step, err)
		}
	}

	// Past the window from the last touch: expired.
	last := now.Add(10 * 9 * time.Minute)
	if _, err := sessions.Validate(context.Background(), issued.Token,
		last.Add(lifetimes.Idle)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("an idle session reported %v rather than ErrSessionExpired", err)
	}

	// AND IT IS NOT RENEWED BY ARRIVING. The row is gone, so a second attempt finds
	// nothing rather than a refreshed window.
	if _, err := sessions.Validate(context.Background(), issued.Token,
		last.Add(lifetimes.Idle+time.Minute)); !errors.Is(err, ErrNoSession) {
		t.Errorf("an expired session reported %v rather than being gone", err)
	}
}

// TestASessionExpiresOnTheAbsoluteCap, with the idle window never allowed to close, so
// only the cap can end it.
func TestASessionExpiresOnTheAbsoluteCap(t *testing.T) {
	rows := newMemorySessions()
	lifetimes := Lifetimes{Idle: 10 * time.Minute, Absolute: 2 * time.Hour}
	sessions := NewSessions(rows, lifetimes)
	now := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	issued, err := sessions.Issue(context.Background(), 1, now)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	for at := now.Add(5 * time.Minute); at.Before(now.Add(lifetimes.Absolute)); at = at.Add(5 * time.Minute) {
		if _, err := sessions.Validate(context.Background(), issued.Token, at); err != nil {
			t.Fatalf("a session kept warm was refused at %s: %v", at, err)
		}
	}

	if _, err := sessions.Validate(context.Background(), issued.Token,
		now.Add(lifetimes.Absolute)); !errors.Is(err, ErrSessionExpired) {
		t.Errorf("a session at its absolute cap reported %v rather than ErrSessionExpired", err)
	}
}

// TestActivityNeverMovesTheAbsoluteCap: the cap is written once, and the whole point
// of it is that use cannot extend it.
func TestActivityNeverMovesTheAbsoluteCap(t *testing.T) {
	rows := newMemorySessions()
	sessions := NewSessions(rows, Lifetimes{Idle: time.Hour, Absolute: 4 * time.Hour})
	now := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	issued, err := sessions.Issue(context.Background(), 1, now)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	absoluteCap := issued.Session.ExpiresAt

	for step := range 3 {
		at := now.Add(time.Duration(step+1) * 30 * time.Minute)
		session, err := sessions.Validate(context.Background(), issued.Token, at)
		if err != nil {
			t.Fatalf("validating: %v", err)
		}
		if session.ExpiresAt != absoluteCap {
			t.Fatalf("use moved the absolute cap from %d to %d", absoluteCap, session.ExpiresAt)
		}
		if session.LastSeenAt != at.Unix() {
			t.Errorf("the idle window did not move: %d", session.LastSeenAt)
		}
	}
}

// TestRevokeRemovesTheRowSoAReplayFindsNothing.
func TestRevokeRemovesTheRowSoAReplayFindsNothing(t *testing.T) {
	rows := newMemorySessions()
	sessions := NewSessions(rows, DefaultLifetimes())
	now := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	issued, err := sessions.Issue(context.Background(), 1, now)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	if err := sessions.Revoke(context.Background(), issued.Token); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	if _, err := sessions.Validate(context.Background(), issued.Token, now); !errors.Is(err, ErrNoSession) {
		t.Errorf("a revoked session reported %v", err)
	}
	if len(rows.rows) != 0 {
		t.Error("revoking left the row behind")
	}
}

// TestAnUnknownOrEmptyTokenIsNoSession.
func TestAnUnknownOrEmptyTokenIsNoSession(t *testing.T) {
	sessions := NewSessions(newMemorySessions(), DefaultLifetimes())
	now := time.Now()
	for name, token := range map[string]string{
		"an empty token":   "",
		"an unknown token": "not-a-token",
	} {
		if _, err := sessions.Validate(context.Background(), token, now); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s reported %v", name, err)
		}
	}
}

// TestCollectExpiredKeepsTheTableBounded.
func TestCollectExpiredKeepsTheTableBounded(t *testing.T) {
	rows := newMemorySessions()
	lifetimes := Lifetimes{Idle: 10 * time.Minute, Absolute: time.Hour}
	sessions := NewSessions(rows, lifetimes)
	now := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	for account := range 5 {
		if _, err := sessions.Issue(context.Background(), int64(account+1), now); err != nil {
			t.Fatalf("issuing: %v", err)
		}
	}
	if len(rows.rows) != 5 {
		t.Fatalf("%d sessions were issued rather than 5", len(rows.rows))
	}
	if err := sessions.CollectExpired(context.Background(), now.Add(2*time.Hour)); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if len(rows.rows) != 0 {
		t.Errorf("%d expired sessions survived the collection", len(rows.rows))
	}
}

// TestTheTwoDefaultLifetimesAreOrderedSensibly: an idle window longer than the
// absolute cap would make the cap the only bound, which is not two bounds.
func TestTheTwoDefaultLifetimesAreOrderedSensibly(t *testing.T) {
	lifetimes := DefaultLifetimes()
	if lifetimes.Idle <= 0 || lifetimes.Absolute <= 0 {
		t.Fatal("a default lifetime is not positive")
	}
	if lifetimes.Idle >= lifetimes.Absolute {
		t.Error("the idle window is not shorter than the absolute cap, so only one bound binds")
	}
}

// TestZeroLifetimesTakeTheDefaults: a caller that passes nothing gets the two bounds
// rather than no bounds at all.
func TestZeroLifetimesTakeTheDefaults(t *testing.T) {
	sessions := NewSessions(newMemorySessions(), Lifetimes{})
	if got := sessions.Lifetimes(); got != DefaultLifetimes() {
		t.Errorf("a zero Lifetimes produced %+v rather than the defaults", got)
	}
}
