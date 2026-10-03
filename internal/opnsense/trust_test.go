package opnsense

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The trust decision, against a server that really does present a certificate no
// system root vouches for.
//
// IT IS TESTED AGAINST A REAL HANDSHAKE AND NOT AGAINST A FABRICATED ERROR. The
// whole defect this file's subject fixes was a misreading of what a transport error
// MEANS, so a test that handed the classifier an error of its own choosing would
// assert the mapping and prove nothing about the thing being mapped. httptest's TLS
// server generates its own certificate, which is the shape of a firewall as OPNsense
// ships it.

// TestAFirewallCertificateIsTrustedONLYWhenItIsPinned is the whole of the trust
// story: refused with no pin, refused under the wrong pin, accepted under the right
// one.
func TestAFirewallCertificateIsTrustedONLYWhenItIsPinned(t *testing.T) {
	firewall := httptest.NewTLSServer(okHandler(t))
	defer firewall.Close()

	actual := CertificateFingerprint(firewall.Certificate())
	// A fingerprint of the right shape that is not this certificate's. Flipping the
	// first character keeps it 64 hex characters, so what is being refused is the
	// VALUE and not the form.
	wrong := flipFirstHexDigit(actual)

	for _, testCase := range []struct {
		name        string
		fingerprint string
		outcome     Outcome
	}{
		{"with nothing pinned, the self-signed certificate is refused", "", OutcomeCertificateRefused},
		{"under the wrong fingerprint it is refused", wrong, OutcomeCertificateRefused},
		{"under its own fingerprint it is accepted", actual, OutcomeOK},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := NewClient(Credentials{
				BaseURL:     firewall.URL,
				APIKey:      generatedCredential(t),
				APISecret:   generatedCredential(t),
				Fingerprint: testCase.fingerprint,
			}, nil, 5*time.Second)

			response, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{})
			if response.Outcome != testCase.outcome {
				t.Fatalf("the outcome is %q, want %q (error: %v)",
					response.Outcome, testCase.outcome, err)
			}
			if testCase.outcome == OutcomeOK {
				if err != nil {
					t.Fatalf("an accepted certificate still produced an error: %v", err)
				}
				return
			}
			// THE OUTCOME IS NOT THE UNREACHABLE HOST, and that is the assertion the
			// defect this test exists for would have failed. The host answered.
			if response.Outcome == OutcomeTransportFailure {
				t.Fatal("a refused certificate was reported as an unreachable host")
			}
		})
	}
}

// TestAPinnedFingerprintDoesNotWeakenTheStandardVerification asserts the other half:
// pinning is what an operator opts into, and an installation that pins nothing is
// verified against the system roots exactly as before. The proof is that the
// self-signed server is refused when nothing is pinned, with a certificate error
// rather than a transport one.
func TestAPinnedFingerprintDoesNotWeakenTheStandardVerification(t *testing.T) {
	firewall := httptest.NewTLSServer(okHandler(t))
	defer firewall.Close()

	client := NewClient(Credentials{
		BaseURL:   firewall.URL,
		APIKey:    generatedCredential(t),
		APISecret: generatedCredential(t),
	}, nil, 5*time.Second)
	response, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{})
	if response.Outcome != OutcomeCertificateRefused {
		t.Fatalf("the outcome is %q, want %q (error: %v)",
			response.Outcome, OutcomeCertificateRefused, err)
	}
	if !certificateRefused(err) {
		t.Fatalf("the error is not recognised as a certificate refusal: %v", err)
	}
}

// TestAChangedFingerprintTakesEffectOnTheNextCall is the keep-alive trap. The
// certificate is checked once per handshake, so a connection opened under a pin that
// matched would keep answering after the pin changed — and the settings surface would
// report a firewall that answers on a trust decision no longer in force.
func TestAChangedFingerprintTakesEffectOnTheNextCall(t *testing.T) {
	firewall := httptest.NewTLSServer(okHandler(t))
	defer firewall.Close()

	live := &mutableCredentials{}
	live.set(Credentials{
		BaseURL:     firewall.URL,
		APIKey:      generatedCredential(t),
		APISecret:   generatedCredential(t),
		Fingerprint: CertificateFingerprint(firewall.Certificate()),
	})
	client := NewClientFromSource(live, nil, 5*time.Second)

	first, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{})
	if first.Outcome != OutcomeOK {
		t.Fatalf("the first call is %q, want %q (error: %v)", first.Outcome, OutcomeOK, err)
	}

	changed := live.Credentials()
	changed.Fingerprint = flipFirstHexDigit(changed.Fingerprint)
	live.set(changed)
	client.CloseIdleConnections()

	second, err := client.Call(context.Background(), InterfacesInfo, RequestOptions{})
	if second.Outcome != OutcomeCertificateRefused {
		t.Fatalf("the call after the pin changed is %q, want %q (error: %v)",
			second.Outcome, OutcomeCertificateRefused, err)
	}
}

// TestParseFingerprintAcceptsWhatAPersonTranscribes covers the forms a fingerprint
// arrives in. OPNsense shows it colon-separated and upper-case, and so does openssl.
func TestParseFingerprintAcceptsWhatAPersonTranscribes(t *testing.T) {
	canonical := strings.Repeat("ab", 32)
	colons := strings.Join(splitEvery(strings.ToUpper(canonical), 2), ":")

	for _, testCase := range []struct {
		name  string
		input string
		want  string
		fails bool
	}{
		{"lower-case hex as stored", canonical, canonical, false},
		{"colon-separated and upper-case, as OPNsense displays it", colons, canonical, false},
		{"spaced", strings.Join(splitEvery(canonical, 4), " "), canonical, false},
		{"empty is empty, which means nothing is pinned", "   ", "", false},
		{"one character short", canonical[:len(canonical)-1], "", true},
		{"not hexadecimal", strings.Repeat("zz", 32), "", true},
		{"an MD5 fingerprint, which is the wrong algorithm", strings.Repeat("ab", 16), "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseFingerprint(testCase.input)
			if testCase.fails {
				if !errors.Is(err, ErrFingerprintMalformed) {
					t.Fatalf("the error is %v, want ErrFingerprintMalformed", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsing %q: %v", testCase.input, err)
			}
			if got != testCase.want {
				t.Fatalf("parsing %q gave %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

// TestAnUnreachableHostIsStillAnUnreachableHost guards the distinction from the other
// side: nothing in the certificate handling may turn a host that does not answer into
// a certificate refusal.
func TestAnUnreachableHostIsStillAnUnreachableHost(t *testing.T) {
	firewall := httptest.NewTLSServer(okHandler(t))
	address := firewall.URL
	fingerprint := CertificateFingerprint(firewall.Certificate())
	firewall.Close()

	client := NewClient(Credentials{
		BaseURL:     address,
		APIKey:      generatedCredential(t),
		APISecret:   generatedCredential(t),
		Fingerprint: fingerprint,
	}, nil, 2*time.Second)
	response, _ := client.Call(context.Background(), InterfacesInfo, RequestOptions{})
	if response.Outcome != OutcomeTransportFailure {
		t.Fatalf("the outcome is %q, want %q", response.Outcome, OutcomeTransportFailure)
	}
}

// flipFirstHexDigit returns a fingerprint of the same shape and a different value.
func flipFirstHexDigit(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	replacement := "0"
	if fingerprint[0] == '0' {
		replacement = "1"
	}
	return replacement + fingerprint[1:]
}

// splitEvery cuts a string into runs of n characters.
func splitEvery(value string, n int) []string {
	var parts []string
	for len(value) > n {
		parts = append(parts, value[:n])
		value = value[n:]
	}
	return append(parts, value)
}

// okHandler answers the verification endpoint with a body the classifier accepts, and
// fails the test if anything else is asked for — so a trust test cannot pass by
// reaching somewhere unintended.
func okHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != InterfacesInfo.Path {
			t.Errorf("the fake firewall was asked for %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"rows":[],"total":0}`))
	}
}
