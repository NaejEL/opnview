// Package providertest is the fake firewall every provider test runs against, and the way to get a
// provider.Session over it.
//
// It lives in a package of its own, rather than in a _test.go file of internal/collect, because an
// implementation's tests live in the implementation's own directory and need it too: a Kea test
// and a coordinator test arrange the same firewall the same way, so the arrangement is written
// once.
//
// THE FAKE IS BUILT FROM A DOCUMENT, AND THAT IS THE LIMIT OF WHAT THESE TESTS PROVE. Every
// response comes from a fixture synthesised by reading docs/opnsense-api-survey.md, each fixture
// says so in its own file, and if the survey is wrong about a field, an envelope or a status code
// these tests pass and the collection is still wrong. Only a run against a real firewall closes
// that.
//
// What the fake does enforce, and what a document cannot get wrong:
//
//   - every path ever requested is a registry entry, so an invented endpoint fails the test
//     rather than reaching a firewall;
//   - the transport refuses any host but the fake, so a second outbound destination fails the
//     test rather than being noticed later;
//   - the credentials are generated per test, so no key or secret exists in the repository to
//     leak.
//
// FIXTURES ARE OWNED BY AN IMPLEMENTATION DIRECTORY and found by file name: a fixture lives in the
// testdata/ directory of the implementation whose answer it imitates (internal/collect/dhcplease/
// kea/testdata/kea_leases.json), and FixtureBody resolves a name by walking every testdata/
// directory below internal/collect. A coordinator test and an implementation test therefore share
// one copy, and a name that exists twice is refused rather than guessed.
package providertest
