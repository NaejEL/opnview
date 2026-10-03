package web

import (
	"html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/secret"
)

// TestNoUserVisibleStringIsALiteral is AC21.
//
// IT IS ASSERTED THREE WAYS, because "no literal" can fail in three places: in the
// template source, in the catalogue's bookkeeping, and in what a handler actually
// emits. The third is the one that matters and the hardest to fake: every text node
// of every rendered page, in every state these surfaces reach, has to BE a catalogue
// string.
func TestNoUserVisibleStringIsALiteral(t *testing.T) {
	catalogue, err := LoadCatalogue(SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}

	t.Run("no template carries bare text", func(t *testing.T) {
		entries, err := files.ReadDir("template")
		if err != nil {
			t.Fatalf("listing the templates: %v", err)
		}
		if len(entries) == 0 {
			t.Fatal("there are no templates, so this assertion is vacuous")
		}
		for _, entry := range entries {
			raw, err := files.ReadFile("template/" + entry.Name())
			if err != nil {
				t.Fatalf("reading %s: %v", entry.Name(), err)
			}
			if left := strings.TrimSpace(stripActions(stripTags(string(raw)))); left != "" {
				t.Errorf("%s carries text outside a tag and outside a catalogue lookup: %q",
					entry.Name(), left)
			}
		}
	})

	t.Run("every entry is reached and every key reached exists", func(t *testing.T) {
		fromTemplates, err := templateKeys()
		if err != nil {
			t.Fatalf("%v", err)
		}
		reached := map[string]bool{}
		for _, key := range fromTemplates {
			reached[key] = true
		}
		for _, key := range handlerMessageKeys() {
			reached[string(key)] = true
		}

		for key := range reached {
			if _, present := catalogue.Strings[key]; !present {
				t.Errorf("the catalogue has no string for %q, which is reached", key)
			}
		}
		var unreachable []string
		for _, key := range catalogue.Keys() {
			if !reached[key] {
				unreachable = append(unreachable, key)
			}
		}
		sort.Strings(unreachable)
		if len(unreachable) > 0 {
			t.Errorf("these catalogue entries are reached from nowhere: %v", unreachable)
		}
	})

	t.Run("every entry is rendered by a state, and no entry duplicates another", func(t *testing.T) {
		// WHY THIS EXISTS BESIDE THE SUBTEST ABOVE. That one counts an entry as reached
		// because a key appears in a template source or in handlerMessageKeys, which is a
		// textual mention: a template branch on a field nothing ever sets, or a constant
		// listed and never chosen, passes it. This one asks the stronger question — does
		// any state these tests can drive actually SHOW this string — and it asks a
		// second one that the first cannot ask at all: are two entries carrying the same
		// words a decision, or a duplicate of an entry that is already there? A key whose
		// wording duplicates a rendered entry's is invisible to a scan of rendered text,
		// and that is exactly how "setup.closed" survived beside "error.setup_closed".
		//
		// WHAT IT DOES NOT PROVE. It compares strings, not keys: it is not a trace of
		// which key each render resolved, so the duplicate half is what stands between a
		// duplicated entry and a false pass. Instrumenting the renderer to record the
		// keys it resolves would prove it directly and would put a test hook in product
		// code, which is not worth it for this.
		shown := map[string]bool{}
		for _, page := range everyRenderedPage(t) {
			for _, line := range visibleLines(page.document) {
				shown[html.UnescapeString(line)] = true
			}
		}

		// The declared exceptions: an entry no state can render, and the reason.
		unrenderable := map[string]string{
			string(msgInternal): "failInternal writes a plain-text body rather than a page, " +
				"because what has failed may be the renderer or the catalogue itself",
		}
		for key := range unrenderable {
			if _, present := catalogue.Strings[key]; !present {
				t.Errorf("%q is declared unrenderable and is in no catalogue", key)
			}
		}
		for key, value := range catalogue.Strings {
			rendered := shown[strings.TrimSpace(value)]
			reason, declared := unrenderable[key]
			switch {
			case rendered && declared:
				t.Errorf("%q is declared unrenderable (%s) and is rendered all the same",
					key, reason)
			case !rendered && !declared:
				t.Errorf("no state renders %q, so it is an entry nothing can show", key)
			}
		}

		// The entries that carry the same words ON PURPOSE: a page's title and its
		// heading are one sentence, the action that leads to a page is the words of the
		// page, and a key file that does not match is one sentence whichever credential
		// it cannot open. Every group is a decision written here; a new entry joining one,
		// or a new pair nobody decided on, fails until somebody says which it is.
		declaredSameWording := [][]string{
			{"setup.title", "setup.heading"},
			{"action.sign_in", "sign_in.title", "sign_in.heading"},
			{"action.reset_password", "recover.title", "recover.heading"},
			{"state.credential.undecryptable", "state.licence_key.undecryptable"},
		}
		declared := map[string]bool{}
		for _, group := range declaredSameWording {
			sorted := append([]string(nil), group...)
			sort.Strings(sorted)
			declared[strings.Join(sorted, " + ")] = true
		}
		byValue := map[string][]string{}
		for key, value := range catalogue.Strings {
			trimmed := strings.TrimSpace(value)
			byValue[trimmed] = append(byValue[trimmed], key)
		}
		for _, keys := range byValue {
			if len(keys) < 2 {
				continue
			}
			sort.Strings(keys)
			name := strings.Join(keys, " + ")
			if !declared[name] {
				t.Errorf("%s carry the same words, which nobody decided on: either one of "+
					"them is the entry the other should reuse, or the group belongs in the "+
					"list of deliberate ones", name)
			}
		}
		// And a declared group that no longer shares its wording is stale rather than
		// harmless: the decision it records is about entries that say the same thing.
		for _, group := range declaredSameWording {
			for _, key := range group {
				value, present := catalogue.Strings[key]
				if !present {
					t.Errorf("%q is declared to share its wording and is in no catalogue", key)
					continue
				}
				first := catalogue.Strings[group[0]]
				if strings.TrimSpace(value) != strings.TrimSpace(first) {
					t.Errorf("%q and %q are declared to say the same thing and no longer do",
						key, group[0])
				}
			}
		}
	})

	t.Run("every text node of every rendered page is a catalogue string", func(t *testing.T) {
		known := map[string]bool{}
		for _, value := range catalogue.Strings {
			known[strings.TrimSpace(value)] = true
		}
		for _, page := range everyRenderedPage(t) {
			// A measured figure is data, not a string: the one exception, and only for
			// a figure in one of the three forms, inside the element that marks it.
			for _, line := range visibleLines(withoutFigures(page.document)) {
				if !known[html.UnescapeString(line)] {
					t.Errorf("%s shows %q, which is in no catalogue", page.name, line)
				}
			}
		}
	})
}

// TestThereIsNoExplanatoryCopyInTheInterface is AC22.
//
// THE PHRASES ARE THE CHARACTERISTIC ONES OF THE TWO STANDING LIMITATIONS, taken from
// README.md and ROADMAP.md. Both limitations are documented, in those files, and
// neither is recited on a screen: ROADMAP.md step 7 forbids explanatory copy, and the
// observation-point limit is explicitly "documented, not printed".
func TestThereIsNoExplanatoryCopyInTheInterface(t *testing.T) {
	forbidden := []string{
		// The observation-point limit.
		"only sees what crosses",
		"crosses the router",
		"lower bound",
		"observation point",
		"switched and never reaches",
		// What the key file does and does not protect.
		"copied, backed up",
		"sent by mistake",
		"already has the machine",
		"does not travel with it",
		// Anything that explains the product rather than naming a thing.
		"this page",
		"in order to",
		"you can",
		"you will",
		"for example",
		"please",
		"note that",
	}

	catalogue, err := LoadCatalogue(SourceLanguage)
	if err != nil {
		t.Fatalf("loading the catalogue: %v", err)
	}

	subjects := map[string]string{}
	for key, value := range catalogue.Strings {
		subjects["the catalogue entry "+key] = value
	}
	rendered := everyRenderedPage(t)
	for _, page := range rendered {
		subjects[page.name+" as rendered"] = visibleText(page.document)
	}
	stylesheet, err := files.ReadFile("asset/style.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	subjects["the stylesheet"] = string(stylesheet)

	for where, content := range subjects {
		lowered := strings.ToLower(content)
		for _, phrase := range forbidden {
			if strings.Contains(lowered, phrase) {
				t.Errorf("%s carries the explanatory phrase %q", where, phrase)
			}
		}
	}

	// A field is named by its label, so no rendered text node is a passage. One
	// sentence is what a state or a refusal gets; two is copy, and so is a label
	// long enough to be a paragraph in disguise.
	for _, page := range rendered {
		for _, line := range visibleLines(page.document) {
			if strings.Count(line, ". ") > 0 {
				t.Errorf("%s shows more than one sentence at once: %q", page.name, line)
			}
			if words := len(strings.Fields(line)); words > 12 {
				t.Errorf("%s shows a %d-word passage, which is copy rather than a label: %q",
					page.name, words, line)
			}
		}
	}
}

// TestNothingIsLoadedFromOffHost is AC23.
func TestNothingIsLoadedFromOffHost(t *testing.T) {
	// THE PATTERNS ARE RESOURCE-LOADING POSITIONS, not the characters of a URL. A
	// settings page legitimately carries an absolute URL: the firewall's, in the field
	// the operator typed it into, which is data rather than something the page fetches.
	// What must not exist is a position from which the browser would GO somewhere:
	// a src, an href, an @import, a url() or a protocol-relative reference.
	offHost := []string{
		`src="http`, `src='http`, `src="//`, `src='//`,
		`href="http`, `href='http`, `href="//`, `href='//`,
		"@import", "url(http", "url(//", "//cdn", "googleapis", "gstatic", "unpkg", "jsdelivr",
	}

	served := map[string]string{}
	stylesheet, err := files.ReadFile("asset/style.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	// The stylesheet's own prose names the things it does not do, so the rules are
	// what is scanned rather than the comments around them.
	served["the stylesheet"] = stripCSSComments(string(stylesheet))
	for _, page := range everyRenderedPage(t) {
		served[page.name+" as served"] = page.document
	}

	for where, content := range served {
		for _, marker := range offHost {
			if strings.Contains(content, marker) {
				t.Errorf("%s carries %q, which would load from another host", where, marker)
			}
		}
		// And nothing but the one stylesheet is referenced at all, so a font, an icon
		// set, an image or a script cannot have been added without this failing.
		for _, marker := range []string{"<script", "<img", "<iframe", "@font-face", "url("} {
			if strings.Contains(content, marker) {
				t.Errorf("%s carries %q", where, marker)
			}
		}
	}

	// The one thing a page does load is the stylesheet this binary serves, by a
	// relative path. A page that loaded nothing at all would pass the checks above
	// without proving anything about them.
	for _, page := range everyRenderedPage(t) {
		if !strings.Contains(page.document, `href="`+PathStylesheet+`"`) {
			t.Errorf("%s does not load the stylesheet this binary serves", page.name)
		}
	}
}

// stripCSSComments removes /* … */ from a stylesheet, so what is scanned is the rules
// rather than the prose explaining them.
func stripCSSComments(source string) string {
	var builder strings.Builder
	for {
		start := strings.Index(source, "/*")
		if start < 0 {
			builder.WriteString(source)
			return builder.String()
		}
		builder.WriteString(source[:start])
		end := strings.Index(source[start:], "*/")
		if end < 0 {
			return builder.String()
		}
		source = source[start+end+2:]
	}
}

// TestTheInterfaceFollowsTheOperatingSystemUntilItIsOverridden is AC24's
// theme half, and the rule is ROADMAP.md step 3's and docs/ui-references.md's rather
// than a new one.
func TestTheInterfaceFollowsTheOperatingSystemUntilItIsOverridden(t *testing.T) {
	harness := newHarness(t)
	harness.completeSetup()

	// First launch: no override, so no data-theme attribute, so the stylesheet's
	// prefers-color-scheme block is what decides.
	first := pageSource(t, harness, PathSettings)
	if strings.Contains(first, "data-theme=") {
		t.Error("a first launch pins a theme rather than following the operating system")
	}

	// The override, and it persists as a setting row.
	assertStatus(t, harness.post(PathSettings, url.Values{"theme": {string(ThemeDark)}}),
		http.StatusOK)
	overridden := pageSource(t, harness, PathSettings)
	if !strings.Contains(overridden, `data-theme="industrial-dark"`) {
		t.Error("the override was not applied to the document")
	}

	// A RESTART KEEPS IT. An override that lived in memory would be gone here.
	restarted := harness.reopen()
	if attribute := restarted.server.readTheme(t.Context()).DocumentAttribute(); attribute != "industrial-dark" {
		t.Errorf("the override did not persist across a restart: %q", attribute)
	}

	// And the stylesheet carries the rule in both directions: the media query for the
	// operating system, and the attribute selectors for the override.
	stylesheet, err := files.ReadFile("asset/style.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	for _, required := range []string{
		"prefers-color-scheme: dark",
		`:root[data-theme="industrial-dark"]`,
		`:root[data-theme="industrial-light"]`,
		// The industrial palette's own tokens, which is what AC24 names: the
		// 0.25rem radius and system fonts.
		"--border-radius: 0.25rem",
		"-apple-system",
	} {
		if !strings.Contains(string(stylesheet), required) {
			t.Errorf("the stylesheet does not carry %q", required)
		}
	}
	// No web font, which is what "system fonts" means in enforceable form.
	if strings.Contains(string(stylesheet), "fonts.googleapis") ||
		strings.Contains(string(stylesheet), "@font-face") {
		t.Error("the stylesheet loads a font rather than using system fonts")
	}

	// THE LINK TOKENS, IN EVERY COLOUR BLOCK, at the values docs/ui-references.md
	// publishes for Family C — its table names the row --link-color / --link-hover, and
	// the token name here is the step-3 mockup's --link, which is the vocabulary this
	// stylesheet's own header says it shares with the canvas. A link styled with
	// --accent instead resolves to #3a7ca5 in low light where the document says
	// #5a9cc8, which is a divergence nothing on screen would announce.
	for _, block := range []struct {
		selector  string
		link      string
		linkHover string
	}{
		{":root {", "#2c5f8d", "#1e4164"},
		{`:root:not([data-theme="industrial-light"]) {`, "#5a9cc8", "#7eb8db"},
		{`:root[data-theme="industrial-light"] {`, "#2c5f8d", "#1e4164"},
		{`:root[data-theme="industrial-dark"] {`, "#5a9cc8", "#7eb8db"},
	} {
		body := paletteBlock(t, string(stylesheet), block.selector)
		for property, value := range map[string]string{
			"--link":       block.link,
			"--link-hover": block.linkHover,
		} {
			declaration := property + ": " + value + ";"
			if !strings.Contains(body, declaration) {
				t.Errorf("the %s block does not declare %q", block.selector, declaration)
			}
		}
	}
	// And a link takes them, rather than the colour of a control.
	for _, required := range []string{"color: var(--link);", "color: var(--link-hover);"} {
		if !strings.Contains(string(stylesheet), required) {
			t.Errorf("no rule carries %q, so a link is not styled with the link tokens",
				required)
		}
	}
}

// paletteBlock returns the body of the LAST block opened by selector, and fails
// unless it is a palette block.
//
// The last, because ":root {" opens the non-colour tokens before it opens the default
// palette, and it is the palette that carries a colour. The --accent check is what
// makes a wrong block a failure rather than a silent pass on a block that simply has
// no link in it.
func paletteBlock(t *testing.T, stylesheet, selector string) string {
	t.Helper()
	start := strings.LastIndex(stylesheet, selector)
	if start < 0 {
		t.Fatalf("the stylesheet has no %s block", selector)
	}
	rest := stylesheet[start+len(selector):]
	end := strings.Index(rest, "}")
	if end < 0 {
		t.Fatalf("the %s block is never closed", selector)
	}
	body := rest[:end]
	if !strings.Contains(body, "--accent:") {
		t.Fatalf("the block found for %s declares no --accent, so it is not a palette", selector)
	}
	return body
}

// renderedPage is one surface in one state, named so a failure says which.
type renderedPage struct {
	name     string
	document string
}

// everyRenderedPage renders each surface in each state these tests can reach, so the
// assertions above run against what actually ships rather than against a template.
//
// THE REFUSALS ARE IN IT AS WELL AS THE HAPPY PAGES. A literal, a sentence of copy or
// an off-host reference is at least as likely to arrive in an error path, and an
// error path is the one nobody looks at.
func everyRenderedPage(t *testing.T) []renderedPage {
	t.Helper()
	var pages []renderedPage
	add := func(name string, answer *http.Response) {
		t.Helper()
		_, body := statusAndBody(t, answer)
		pages = append(pages, renderedPage{name, body})
	}
	long := randomHex(t, 12)

	// Before an account exists: the setup surface, the sign-in surface and the reset
	// surface.
	fresh := newHarness(t)
	pages = append(pages,
		renderedPage{"the setup surface", pageSource(t, fresh, PathSetup)},
		renderedPage{"the sign-in surface", pageSource(t, fresh, PathSignIn)},
		renderedPage{"the reset surface", pageSource(t, fresh, PathRecover)})

	// EVERY WAY THE SETUP SURFACE REFUSES, one page each. None of these creates an
	// account, so the surface is still open after all of them.
	add("a refused setup", fresh.post(PathSetup, url.Values{
		"setup_token": {fresh.setupToken.Token()},
		"login":       {"operator"}, "password": {"short"}, "password_again": {"short"},
	}))
	add("a setup carrying a wrong token", fresh.post(PathSetup, url.Values{
		"setup_token": {randomHex(t, 32)},
		"login":       {"operator"}, "password": {long}, "password_again": {long},
	}))
	add("a setup with no login", fresh.post(PathSetup, url.Values{
		"setup_token": {fresh.setupToken.Token()},
		"login":       {""}, "password": {long}, "password_again": {long},
	}))
	add("a setup with no password", fresh.post(PathSetup, url.Values{
		"setup_token": {fresh.setupToken.Token()},
		"login":       {"operator"}, "password": {""}, "password_again": {""},
	}))
	add("a setup whose two passwords differ", fresh.post(PathSetup, url.Values{
		"setup_token": {fresh.setupToken.Token()},
		"login":       {"operator"}, "password": {long}, "password_again": {long + "x"},
	}))
	// The form token missing, which the middleware refuses before the handler exists.
	_ = fresh.formToken(PathSetup)
	add("a setup with no form token", fresh.postRaw(PathSetup, url.Values{
		"setup_token": {fresh.setupToken.Token()},
		"login":       {"operator"}, "password": {long}, "password_again": {long},
	}))

	// A refused reset: the key file was not presented.
	add("a refused reset", fresh.post(PathRecover, url.Values{
		"login": {"operator"}, "password": {randomHex(t, 12)},
		"password_again": {randomHex(t, 12)},
	}))

	// After an account exists: the settings surface, before and after credentials.
	configured := newHarness(t)
	login, password := configured.completeSetup()
	pages = append(pages,
		renderedPage{"the settings surface", pageSource(t, configured, PathSettings)})

	// The setup surface once an account exists. The GET redirects and has no body, so
	// the page this state renders is the POST's: the permanent closure, on the sign-in
	// surface.
	add("a setup refused because the account exists", configured.postRaw(PathSetup, url.Values{
		"csrf_token":  {configured.formToken(PathSettings)},
		"setup_token": {configured.setupToken.Token()},
		"login":       {"second-" + randomHex(t, 4)},
		"password":    {long}, "password_again": {long},
	}))

	apiKey := randomHex(t, 12)
	add("the settings surface after a save", configured.post(PathSettings, url.Values{
		"firewall_url":        {configured.fake.baseURL()},
		"api_key":             {apiKey},
		"api_secret":          {randomHex(t, 16)},
		"maxmind_licence_key": {randomHex(t, 16)},
		"theme":               {string(ThemeSystem)},
	}))

	// Every way the settings surface refuses a submission.
	add("a refused settings submission", configured.post(PathSettings, url.Values{
		"firewall_url": {"not-a-url"}, "theme": {string(ThemeSystem)},
	}))
	add("a settings submission with a secret and no key beside it",
		configured.post(PathSettings, url.Values{
			"api_secret": {randomHex(t, 16)}, "theme": {string(ThemeSystem)},
		}))
	add("a settings submission naming a theme that does not exist",
		configured.post(PathSettings, url.Values{"theme": {"no-such-theme"}}))
	add("a settings submission whose fingerprint is not a fingerprint",
		configured.post(PathSettings, url.Values{
			"certificate_fingerprint": {"not-a-fingerprint"},
			"theme":                   {string(ThemeSystem)},
		}))

	// EVERY VERIFICATION OUTCOME, because each is a separate sentence and an error path
	// is the one nobody looks at. The unreachable host comes last of the firewall
	// states: it stops the fake answering for good.
	for _, outcome := range []struct {
		name   string
		status int
	}{
		{"the settings surface after a rejected credential", http.StatusUnauthorized},
		{"the settings surface after an absent endpoint", http.StatusNotFound},
		{"the settings surface after an answer it cannot use", http.StatusInternalServerError},
	} {
		configured.fake.answer(outcome.status, []byte(`{}`))
		add(outcome.name, configured.storeCredentials(apiKey, randomHex(t, 16)))
	}
	configured.fake.goDown()
	add("the settings surface with the firewall unreachable",
		configured.storeCredentials(apiKey, randomHex(t, 16)))

	// THE CERTIFICATE REFUSED, WHICH IS NOT THE HOST UNREACHABLE. On its own harness
	// because its firewall speaks TLS: this is the only state in this list where
	// opnview decides whether to believe a certificate, and the state the product
	// used to report as the line above.
	pinned := newTLSHarness(t)
	pinned.completeSetup()
	add("the settings surface with the firewall's certificate refused",
		pinned.storeCredentials(randomHex(t, 12), randomHex(t, 16)))
	add("the settings surface with no firewall URL",
		configured.post(PathSettings, url.Values{"theme": {string(ThemeSystem)}}))

	// A refused sign-in.
	anonymous := freshClient(t, configured)
	add("a refused sign-in", anonymous.post(PathSignIn,
		url.Values{"login": {"nobody"}, "password": {randomHex(t, 12)}}))

	// The states a session produces, on a harness of their own so their order cannot
	// disturb the settings states above. Each is a redirect carrying a message, so the
	// page that renders it is the one the reader lands on.
	busy := newHarness(t)
	busyLogin, busyPassword := busy.completeSetup()
	discard(t, busy.post(PathSignOut, url.Values{}))
	pages = append(pages,
		renderedPage{"the sign-in surface after signing out", pageSource(t, busy, PathSignIn)})

	busy.signIn(busyLogin, busyPassword)
	busy.clock.advance(2 * auth.DefaultLifetimes().Absolute)
	discard(t, busy.get(PathSettings))
	pages = append(pages,
		renderedPage{"the sign-in surface after a session ended", pageSource(t, busy, PathSignIn)})

	keyFile, err := os.ReadFile(secret.KeyPath(busy.dataDir))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}
	replacement := randomHex(t, 12)
	discard(t, busy.post(PathRecover, url.Values{
		"login": {busyLogin}, "key_file": {string(keyFile)},
		"password": {replacement}, "password_again": {replacement},
	}))
	pages = append(pages,
		renderedPage{"the sign-in surface after a password reset", pageSource(t, busy, PathSignIn)})

	// The key file gone with the database intact, which is the state AC14 names.
	lost := reopenWithoutKeyFile(t, configured)
	pages = append(pages,
		renderedPage{"the sign-in surface with no key file", pageSource(t, lost, PathSignIn)})
	lost.signIn(login, password)
	pages = append(pages,
		renderedPage{"the settings surface with no key file", pageSource(t, lost, PathSettings)})

	// THE PRESENTED CERTIFICATE, fetched and not fetched. The fetch is a TLS
	// handshake and nothing else, so the fake that speaks TLS presents one and the
	// fake that does not cannot.
	fetched := pinned.post(PathSettings, url.Values{
		"firewall_url": {pinned.fake.baseURL()}, "fetch_certificate": {"1"},
		"theme": {string(ThemeSystem)},
	})
	_, body := statusAndBody(t, fetched)
	if !strings.Contains(body, pinned.fake.fingerprint()) {
		t.Error("the fetched certificate's fingerprint is not in the field it fills")
	}
	pages = append(pages, renderedPage{"the settings surface with the presented certificate", body})
	plain := newHarness(t)
	plain.completeSetup()
	add("the settings surface with no certificate presented", plain.post(PathSettings, url.Values{
		"firewall_url": {plain.fake.baseURL()}, "fetch_certificate": {"1"},
		"theme": {string(ThemeSystem)},
	}))
	add("the settings surface fetching with no firewall URL", plain.post(PathSettings, url.Values{
		"fetch_certificate": {"1"}, "theme": {string(ThemeSystem)},
	}))

	pages = append(pages, collectionPages(t)...)
	pages = append(pages, geoIPPages(t)...)

	if len(pages) == 0 {
		t.Fatal("no page was rendered, so these assertions are vacuous")
	}
	return pages
}

// figureForms are the three forms a measured figure takes on a page: a whole
// number, a rate to three decimals, and an instant in RFC 3339, UTC.
var figureForms = []*regexp.Regexp{
	regexp.MustCompile(`^[0-9]+$`),
	regexp.MustCompile(`^[0-9]+\.[0-9]{3}$`),
	regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`),
}

// figureSpan is the element a template marks a figure with.
var figureSpan = regexp.MustCompile(`<span class="figure">([^<]*)</span>`)

// isFigure says whether a text is a figure in one of the three forms.
func isFigure(text string) bool {
	for _, form := range figureForms {
		if form.MatchString(text) {
			return true
		}
	}
	return false
}

// withoutFigures removes every marked figure in one of the three forms from a page,
// and nothing else: a figure in another form, or a figure outside the element that
// marks it, stays, and then has to be a catalogue string like any other text.
func withoutFigures(document string) string {
	return figureSpan.ReplaceAllStringFunc(document, func(span string) string {
		if isFigure(figureSpan.FindStringSubmatch(span)[1]) {
			return ""
		}
		return span
	})
}

// TestOnlyTheThreeFigureFormsThePagesProducePassTheFigureException holds the
// exception to what it is for: the three forms, marked, and nothing else — and
// every figure the pages actually mark is in one of them.
func TestOnlyTheThreeFigureFormsThePagesProducePassTheFigureException(t *testing.T) {
	for _, figure := range []string{"0", "86400", "0.125", "12.000", "2026-03-14T09:00:00Z"} {
		if withoutFigures(`<p><span class="figure">`+figure+`</span></p>`) != "<p></p>" {
			t.Errorf("the figure %q does not pass the exception", figure)
		}
	}
	for _, text := range []string{
		"", "-3", "1.5", "0.1250", "1e3", "NaN", "12 records", "3,5",
		"2026-03-14 09:00:00", "2026-03-14T09:00:00+01:00", "2026-03-14T09:00:00.5Z",
		"Saved",
	} {
		marked := `<span class="figure">` + text + `</span>`
		if withoutFigures(marked) != marked {
			t.Errorf("%q passes the exception and is not one of the three forms", text)
		}
	}
	if unmarked := "<dd>42</dd>"; withoutFigures(unmarked) != unmarked {
		t.Error("a figure outside the element that marks it passes the exception")
	}

	marked := 0
	for _, page := range collectionPages(t) {
		for _, match := range figureSpan.FindAllStringSubmatch(page.document, -1) {
			marked++
			if !isFigure(match[1]) {
				t.Errorf("%s marks %q as a figure, which is none of the three forms",
					page.name, match[1])
			}
		}
	}
	if marked == 0 {
		t.Fatal("no page marks a figure, so this assertion is vacuous")
	}
}

// discard reads and closes a response nothing asserts on. It is the redirect that
// carries a message to the page the reader lands on: the body is empty and the page
// that matters is the next one.
func discard(t *testing.T, answer *http.Response) {
	t.Helper()
	_, _ = statusAndBody(t, answer)
}

// stripTags removes everything between < and >.
func stripTags(source string) string { return visibleText(source) }

// stripActions removes everything between {{ and }}, comments included.
func stripActions(source string) string {
	var builder strings.Builder
	for {
		start := strings.Index(source, "{{")
		if start < 0 {
			builder.WriteString(source)
			return builder.String()
		}
		builder.WriteString(source[:start])
		end := strings.Index(source[start:], "}}")
		if end < 0 {
			return builder.String()
		}
		source = source[start+end+2:]
	}
}
