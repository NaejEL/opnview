package auth

import (
	"crypto/subtle"
	"errors"
	"sync"
)

// The one-time setup token.
//
// WHY IT EXISTS. The product ships into an LXC reachable from the network, and a
// setup surface that is public by construction is a race anybody on the local
// network can win: the first request creates the account and owns the
// installation. The token narrows that to whoever can read one line of the
// service's own console output, which is a step the operator already takes.
//
// WHAT IT IS NOT. It is not what closes the setup surface. The token is printed
// again on a restart that finds no account, so a restart does not hand anybody a
// second chance at an installation that already has one: what closes setup
// PERMANENTLY is the existence of an account, counted from the database on every
// request and on every method. If those two were the same mechanism, a restart
// would reopen setup, which is precisely the hole this cycle's stated top risk
// names.
//
// IT IS NEVER WRITTEN INTO THE DATABASE and never appears in a response body. It
// lives in this value, in memory, for the life of the process.

// ErrSetupTokenRefused is returned for a missing, wrong or already-spent token.
// The three are one error on purpose: telling them apart tells a caller which
// half of a guess was right.
var ErrSetupTokenRefused = errors.New("auth: the setup token was refused")

// SetupToken is a one-time token held in memory.
type SetupToken struct {
	mutex sync.Mutex
	token string
	spent bool
}

// NewSetupToken generates a token. The caller prints it; nothing else may.
func NewSetupToken() (*SetupToken, error) {
	token, err := RandomToken()
	if err != nil {
		return nil, err
	}
	return &SetupToken{token: token}, nil
}

// Token returns the token, so the caller can print it to the console once.
func (s *SetupToken) Token() string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.token
}

// Check reports whether presented is the live token, without spending it.
//
// The comparison is constant-time, and a spent token fails it however correct the
// value is.
func (s *SetupToken) Check(presented string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.spent || s.token == "" || presented == "" {
		return ErrSetupTokenRefused
	}
	if subtle.ConstantTimeCompare([]byte(presented), []byte(s.token)) != 1 {
		return ErrSetupTokenRefused
	}
	return nil
}

// Spend marks the token used. It is called after setup has actually created the
// account, not before: a valid token that met a rejected password would otherwise
// be burnt by a typo and the operator would have to restart the service.
func (s *SetupToken) Spend() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.spent = true
}

// Spent reports whether the token has been used.
func (s *SetupToken) Spent() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.spent
}
