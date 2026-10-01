package opnsense

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// mutableCredentials is a CredentialSource a test changes between calls. It stands in
// for internal/config's live holder, which is what the product passes.
type mutableCredentials struct {
	mutex sync.RWMutex
	value Credentials
	// reads counts how many times the client asked. A client that read the
	// credentials once, at construction, would leave this at one however many calls
	// it made — which is exactly the defect this seam exists to close.
	reads int
}

// Credentials returns the current value and records the read.
func (m *mutableCredentials) Credentials() Credentials {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.reads++
	return m.value
}

// set replaces the value.
func (m *mutableCredentials) set(value Credentials) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.value = value
}

// readCount is how many times the client asked.
func (m *mutableCredentials) readCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.reads
}

// TestTheClientReadsItsCredentialsOnEveryCall is the transport half of AC16.
//
// IT IS WHAT MAKES A TYPO RECOVERABLE WITHOUT A RESTART. Cycle 4A built the client
// once at start-up with an empty Credentials value, so nothing entered afterwards could
// reach a running collector. The source is read per call instead, and this asserts
// exactly that: the value changes between two calls and the second call carries the new
// one.
func TestTheClientReadsItsCredentialsOnEveryCall(t *testing.T) {
	var seen []string
	firewall := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			_, secret, _ := request.BasicAuth()
			seen = append(seen, secret)
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(`{"rows":[]}`))
		}))
	defer firewall.Close()

	// Both credentials are GENERATED HERE, so none exists in the repository. The base
	// URL comes from the fake's own address, which the operating system chose.
	firstKey, firstSecret := generatedCredential(t), generatedCredential(t)
	secondSecret := generatedCredential(t)
	if secondSecret == firstSecret {
		t.Fatal("the two generated secrets collided, so this proves nothing")
	}

	source := &mutableCredentials{}
	source.set(Credentials{BaseURL: firewall.URL, APIKey: firstKey, APISecret: firstSecret})
	client := NewClientFromSource(source, nil, 5*time.Second)

	if _, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{}); err != nil {
		t.Fatalf("the first call failed: %v", err)
	}

	source.set(Credentials{BaseURL: firewall.URL, APIKey: firstKey, APISecret: secondSecret})
	if _, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{}); err != nil {
		t.Fatalf("the second call failed: %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("the firewall saw %d calls rather than two", len(seen))
	}
	if seen[0] != firstSecret {
		t.Error("the first call did not carry the secret in force when it was made")
	}
	if seen[1] != secondSecret {
		t.Error("the second call did not carry the changed secret")
	}
	if source.readCount() < 2 {
		t.Errorf("the client read its credentials %d times for two calls, so it cached them",
			source.readCount())
	}
}

// TestAnEmptyBaseURLIsStillTheNormalStartingState: an installation nobody has
// configured reports that it has no firewall to talk to, and the source being live does
// not change that.
func TestAnEmptyBaseURLIsStillTheNormalStartingState(t *testing.T) {
	source := &mutableCredentials{}
	client := NewClientFromSource(source, nil, 5*time.Second)
	if _, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{}); err != ErrNoBaseURL {
		t.Errorf("an unconfigured client reported %v rather than ErrNoBaseURL", err)
	}

	// And it recovers the moment a URL arrives, with no new client. The host is in the
	// .invalid top-level domain, which RFC 2606 reserves precisely so that a test can
	// name one that is guaranteed never to resolve.
	source.set(Credentials{BaseURL: "https://firewall.invalid"})
	if _, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{}); err == ErrNoBaseURL {
		t.Error("a client given a URL still reports that it has none")
	}
}

// TestNewClientStillTakesAFixedValue: every existing caller passes one, and this cycle
// added the source without removing that.
func TestNewClientStillTakesAFixedValue(t *testing.T) {
	client := NewClient(Credentials{}, nil, 0)
	if _, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{}); err != ErrNoBaseURL {
		t.Errorf("a client built from a fixed empty value reported %v", err)
	}
	if source := Static(Credentials{BaseURL: "https://firewall.invalid"}); source.Credentials().BaseURL == "" {
		t.Error("Static did not carry the value it was given")
	}
}

// TestANilSourceIsTreatedAsNoCredentials: a caller that passes nothing gets the
// unconfigured state rather than a panic on the first pass.
func TestANilSourceIsTreatedAsNoCredentials(t *testing.T) {
	client := NewClientFromSource(nil, nil, 0)
	if _, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{}); err != ErrNoBaseURL {
		t.Errorf("a client with no source reported %v rather than ErrNoBaseURL", err)
	}
}

// generatedCredential returns a credential for one test run, so nothing
// credential-shaped is written into the repository.
func generatedCredential(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a test credential: %v", err)
	}
	return hex.EncodeToString(raw)
}
