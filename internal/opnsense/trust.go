package opnsense

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Trusting the certificate the firewall presents.
//
// WHY THIS EXISTS AT ALL. OPNsense serves its web GUI, and therefore its API, under
// a SELF-SIGNED certificate on a fresh installation: that is the state of the
// product as it ships, not a misconfiguration. Go's default transport refuses such
// a certificate, so before this file opnview could not talk to a default OPNsense
// at all — and the refusal arrived as a transport error, which the settings surface
// reported as "the host was not reached" while the host had in fact answered the
// handshake. The diagnosis was the opposite of the truth.
//
// WHAT IS TRUSTED IS AN EXPLICIT FINGERPRINT, AND NOTHING ELSE IS WEAKENED. The
// operator reads the SHA-256 fingerprint of the firewall's certificate from
// OPNsense's own interface and types it beside the URL. A certificate whose
// fingerprint is that one is accepted; every other certificate is refused. There is
// no "do not verify" setting in this product, and no code path accepts an
// unrecognised certificate.
//
// AN INSTALLATION WITH A PROPERLY SIGNED CERTIFICATE PINS NOTHING. With no
// fingerprint configured, the standard verification applies unchanged — system
// roots, chain, host name, expiry. That is the reason there are two transports
// below rather than one: pinning is what an operator opts into for the certificate
// OPNsense generated itself, and it is not imposed on an installation that already
// has a certificate a certificate authority vouches for. Pinning such a
// certificate would break collection silently on the day it is renewed.
//
// THE FINGERPRINT IS NOT A SECRET. It is a hash of a certificate the firewall
// hands to anybody who connects, so it is a `setting` row like the URL and not an
// `encrypted_credential`. It is pre-filled in the settings surface for the same
// reason the URL is: a reader correcting a typo has to be able to see the typo.

// FingerprintLength is the number of hex characters in a SHA-256 fingerprint.
const FingerprintLength = sha256.Size * 2

// The certificate refusals. Both are conditions of the firewall's certificate
// rather than programming errors, and both happen during the TLS handshake, so
// neither can carry an HTTP status.
var (
	// ErrFingerprintMismatch is returned when the firewall presented a certificate
	// that is not the pinned one. It is NOT reported as an unreachable host: the
	// host answered, and what failed is the identity it proved.
	ErrFingerprintMismatch = errors.New("opnsense: the firewall's certificate does not match the pinned fingerprint")
	// ErrNoCertificate is returned when a handshake produced no certificate at all.
	// It should not happen against anything that is a TLS server, and accepting it
	// would be accepting an unidentified peer.
	ErrNoCertificate = errors.New("opnsense: the firewall presented no certificate")
	// ErrFingerprintMalformed is returned by ParseFingerprint. It is a refusal of
	// what was typed, so it belongs to whatever is doing the typing rather than to
	// a call.
	ErrFingerprintMalformed = errors.New("opnsense: the certificate fingerprint is not 64 hexadecimal characters")
)

// ParseFingerprint normalises a SHA-256 fingerprint as a person transcribes it.
//
// IT ACCEPTS WHAT A TERMINAL PRINTS AND WHAT A BROWSER SHOWS. `openssl x509
// -fingerprint -sha256` prints it colon-separated and upper-case, and so does every
// browser's certificate viewer.
//
// WHERE OPNSENSE ITSELF PUTS IT IS NOT RECORDED HERE, because it is not verified.
// Certificates are managed under System → Trust → Certificates, which the
// documentation establishes; that a SHA-256 fingerprint is DISPLAYED there is not
// something this project has checked, and docs/opnsense-api-survey.md does not cover
// the GUI. Asking the operator to go and find it is in any case the ergonomic
// problem to solve rather than a step to document — see the note on the settings
// surface.
// Colons, spaces and case are transcription, not content, so they are removed
// rather than refused; anything left that is not 64 hex characters is refused,
// because a fingerprint that is nearly right is a fingerprint that will refuse
// every certificate with no explanation.
func ParseFingerprint(value string) (string, error) {
	cleaned := strings.ToLower(strings.Map(func(r rune) rune {
		switch r {
		case ':', ' ', '-', '\t', '\n', '\r':
			return -1
		}
		return r
	}, value))
	if cleaned == "" {
		return "", nil
	}
	if len(cleaned) != FingerprintLength {
		return "", ErrFingerprintMalformed
	}
	if _, err := hex.DecodeString(cleaned); err != nil {
		return "", ErrFingerprintMalformed
	}
	return cleaned, nil
}

// CertificateFingerprint is the SHA-256 fingerprint of one certificate, lower-case
// hex. It is the value the operator is asked to compare against, so it is computed
// the same way every tool computes it: over the certificate's DER bytes.
func CertificateFingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

// NewTrustingTransport returns the transport the product uses.
//
// It is exported so a test can put the product's OWN trust decision behind its own
// guard — the web harness wraps it in a transport that refuses any host but its
// fake — rather than asserting against a transport the test built itself.
//
// IT READS THE FINGERPRINT PER REQUEST, like the client reads the credentials, so
// an operator who corrects a mistyped fingerprint in the settings surface is
// talking to the firewall on the next collection pass with no restart. The TLS
// configuration itself cannot be changed per request, which is why there are two
// transports and a choice in front of them rather than one transport being
// mutated.
func NewTrustingTransport(source CredentialSource) http.RoundTripper {
	return &trustingTransport{
		source:   source,
		standard: &http.Transport{},
		pinned: &http.Transport{
			TLSClientConfig: &tls.Config{
				// INSECURESKIPVERIFY IS NOT "DO NOT VERIFY" HERE, and it is the only
				// way to say what this needs to say. It switches off the verification
				// Go would do against the system roots — which is exactly the
				// verification that cannot succeed against a certificate OPNsense
				// signed itself — and VerifyPeerCertificate below then supplies the
				// WHOLE trust decision. The callback accepts one certificate and no
				// other, so the connection is pinned rather than unverified. Removing
				// the callback would turn this into the thing its name suggests, which
				// is why nothing may set this field without one.
				InsecureSkipVerify:    true,
				VerifyPeerCertificate: verifyAgainst(source),
				MinVersion:            tls.VersionTLS12,
			},
		},
	}
}

// trustingTransport picks the standard transport or the pinning one.
type trustingTransport struct {
	source   CredentialSource
	standard http.RoundTripper
	pinned   http.RoundTripper
}

// RoundTrip sends the request through whichever transport the configuration asks
// for. No fingerprint means the standard verification, unchanged.
func (t *trustingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.source.Credentials().Fingerprint == "" {
		return t.standard.RoundTrip(request)
	}
	return t.pinned.RoundTrip(request)
}

// CloseIdleConnections closes both pools.
//
// IT IS WHAT MAKES A CHANGED FINGERPRINT TAKE EFFECT. VerifyPeerCertificate runs
// once per handshake, not once per request, so a connection already established
// under the previous fingerprint would be reused and would keep working after the
// pin was changed to something it does not match. The settings surface calls this
// when it replaces the credentials, so the next call shakes hands again.
func (t *trustingTransport) CloseIdleConnections() {
	type idleCloser interface{ CloseIdleConnections() }
	for _, transport := range []http.RoundTripper{t.standard, t.pinned} {
		if closer, ok := transport.(idleCloser); ok {
			closer.CloseIdleConnections()
		}
	}
}

// verifyAgainst builds the callback that is the entire trust decision for a pinned
// connection.
//
// IT READS THE FINGERPRINT AT HANDSHAKE TIME rather than closing over a value, so
// the pin in force is the pin the operator last saved.
func verifyAgainst(source CredentialSource) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return ErrNoCertificate
		}
		pinned := source.Credentials().Fingerprint
		if pinned == "" {
			// The configuration changed between the transport being chosen and the
			// handshake completing. Refusing is the only safe answer: this transport
			// does no chain verification, so accepting here would accept anything.
			return ErrFingerprintMismatch
		}
		// The LEAF, which is rawCerts[0] by RFC 8446 and RFC 5246 alike, and the
		// certificate whose fingerprint OPNsense displays. Pinning an issuer instead
		// would accept every certificate that issuer ever signs.
		sum := sha256.Sum256(rawCerts[0])
		presented := hex.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(presented), []byte(pinned)) != 1 {
			return fmt.Errorf("%w: it presented %s", ErrFingerprintMismatch, presented)
		}
		return nil
	}
}

// certificateRefused reports whether err is the firewall's certificate being
// refused rather than the firewall not answering.
//
// THE DISTINCTION IS THE WHOLE POINT. A refused certificate means the host answered
// and proved an identity opnview does not accept; an unreachable host means no
// answer at all. They send the operator to two completely different places — one to
// the fingerprint field, the other to the network — and reporting the first as the
// second is the defect this function exists to prevent.
func certificateRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrFingerprintMismatch) || errors.Is(err, ErrNoCertificate) {
		return true
	}
	// The standard transport's refusals, for an installation that pins nothing.
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return true
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}
	var hostname x509.HostnameError
	if errors.As(err, &hostname) {
		return true
	}
	var invalid x509.CertificateInvalidError
	return errors.As(err, &invalid)
}

// FetchCertificateFingerprint connects to the firewall at baseURL, completes a TLS
// handshake, and returns the SHA-256 fingerprint of the certificate it presented.
// It is what the settings surface calls when an operator asks to see the
// certificate before pinning it, so that the fingerprint can be compared with the
// one OPNsense displays instead of typed from it.
//
// THIS IS THE ONE CONNECTION THAT DOES NOT VERIFY THE CERTIFICATE, AND IT TRUSTS
// NOTHING. Its whole purpose is to read a certificate that the standard
// verification would refuse — the self-signed one OPNsense ships with — so the
// verification is off. What makes that safe is what the function does not do: it
// sends no request, no credential and no byte of application data over the
// connection, and it returns nothing but the fingerprint. The result is shown to
// a person, who compares it with the firewall's own interface and decides whether
// to pin it; until that person saves it, no collector trusts it, and every
// connection that carries a credential still goes through NewTrustingTransport.
//
// A URL whose scheme is not https presents no certificate, and is answered with
// ErrNoCertificate rather than a connection attempt. With no port, the https
// default applies. A handshake that fails is an error naming the address, so an
// unreachable host is reported as unreachable.
func FetchCertificateFingerprint(ctx context.Context, baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("opnsense: reading the firewall URL: %w", err)
	}
	if parsed.Scheme != "https" {
		// Plain HTTP has no certificate to read, and an empty or relative URL has
		// no host to read it from.
		return "", ErrNoCertificate
	}
	address := parsed.Host
	if parsed.Port() == "" {
		address = net.JoinHostPort(parsed.Host, "443")
	}

	dialer := tls.Dialer{Config: &tls.Config{
		// Off on purpose, and on this connection only: see the comment above. The
		// handshake is the whole exchange, and its one product is a fingerprint a
		// person checks before anything trusts it.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", fmt.Errorf("opnsense: reaching %s: %w", address, err)
	}
	defer func() { _ = conn.Close() }()

	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", ErrNoCertificate
	}
	return CertificateFingerprint(state.PeerCertificates[0]), nil
}
