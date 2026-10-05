package publicsuffix

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The list and the algorithm, as the list's own format document states them
// (github.com/publicsuffix/list/wiki/Format, "Formal algorithm"):
//
//   - each line is read up to its first whitespace; a line starting with // is a
//     comment; every other non-empty line is a rule;
//   - a rule starting with "!" is an exception rule; "*" may stand for one whole
//     label, in the leftmost position only;
//   - a domain matches a rule when, label by label from the right, every label of
//     the rule is identical to the domain's or is "*";
//   - if an exception rule matches, it prevails, with its leftmost label removed;
//     otherwise the matching rule with the most labels prevails; if none matches,
//     the prevailing rule is "*";
//   - the public suffix is the domain's labels the prevailing rule matched, and THE
//     REGISTRABLE DOMAIN -- the list's own term -- is the public suffix plus one
//     more label. A domain that is itself a public suffix has none.
//
// The domain and the rules are compared in canonical form: lower case, and an
// internationalised label in its Punycode form (RFC 3492), because the list writes
// such rules in Unicode and a resolver reports names in either form. The domain's
// labels are returned as they were given, so a name given in Unicode comes back in
// Unicode.

// The section markers every copy of the list carries. A download that lacks them is
// not the list, whatever its status code said.
const (
	icannBegin   = "===BEGIN ICANN DOMAINS==="
	icannEnd     = "===END ICANN DOMAINS==="
	privateBegin = "===BEGIN PRIVATE DOMAINS==="
)

// ErrMalformed is a download that does not hold the list.
var ErrMalformed = errors.New("publicsuffix: the download is not the Public Suffix List")

// List is a parsed copy of the list.
type List struct {
	rules      map[string]struct{}
	wildcards  map[string]struct{}
	exceptions map[string]struct{}
	// Version is the list's own VERSION line, when it carries one.
	Version string
}

// Rules is how many rules the list holds.
func (l *List) Rules() int { return len(l.rules) + len(l.wildcards) + len(l.exceptions) }

// minimumRules is the least a real copy holds; the list has thousands. A file that
// carries the markers and almost nothing between them is a truncated download.
const minimumRules = 10

// Parse reads a copy of the list, refusing anything that is not one: a file
// without the section markers, or with too few rules to be the list.
func Parse(data []byte) (*List, error) {
	return parse(data, minimumRules)
}

// parse is Parse with the floor supplied, which is how a test reads an excerpt.
func parse(data []byte, floor int) (*List, error) {
	if !bytes.Contains(data, []byte(icannBegin)) || !bytes.Contains(data, []byte(icannEnd)) ||
		!bytes.Contains(data, []byte(privateBegin)) {
		return nil, fmt.Errorf("%w: the section markers are missing", ErrMalformed)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: the file is not UTF-8", ErrMalformed)
	}
	list := &List{
		rules:      map[string]struct{}{},
		wildcards:  map[string]struct{}{},
		exceptions: map[string]struct{}{},
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "// VERSION:") {
			list.Version = strings.TrimSpace(strings.TrimPrefix(line, "// VERSION:"))
			continue
		}
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		rule := strings.Fields(line)[0]
		exception := strings.HasPrefix(rule, "!")
		rule = strings.TrimPrefix(rule, "!")
		wildcard := strings.HasPrefix(rule, "*.")
		if wildcard {
			rule = strings.TrimPrefix(rule, "*.")
		}
		canonical, ok := canonicalName(rule)
		if !ok || strings.Contains(canonical, "*") {
			// A rule the format does not allow -- a wildcard anywhere but the leftmost
			// label -- is skipped rather than half-applied.
			continue
		}
		switch {
		case exception:
			list.exceptions[canonical] = struct{}{}
		case wildcard:
			list.wildcards[canonical] = struct{}{}
		default:
			list.rules[canonical] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if list.Rules() < floor {
		return nil, fmt.Errorf("%w: it holds %d rules", ErrMalformed, list.Rules())
	}
	return list, nil
}

// RegistrableDomain returns the registrable domain of a name, and whether it has
// one. A name with a leading dot or an empty label has none; a name that is itself
// a public suffix has none. A trailing dot is kept: example.com. and example.com
// are different names, as the format document asks.
func (l *List) RegistrableDomain(name string) (string, bool) {
	trailingDot := strings.HasSuffix(name, ".")
	trimmed := strings.TrimSuffix(name, ".")
	if trimmed == "" {
		return "", false
	}
	original := strings.Split(strings.ToLower(trimmed), ".")
	labels := make([]string, len(original))
	for index, label := range original {
		if label == "" {
			return "", false
		}
		ascii, ok := toASCII(label)
		if !ok {
			return "", false
		}
		labels[index] = ascii
	}

	count := len(labels)
	suffix := 0
	exceptionFound := false
	for length := count; length >= 1; length-- {
		candidate := strings.Join(labels[count-length:], ".")
		if _, present := l.exceptions[candidate]; present {
			suffix = length - 1
			exceptionFound = true
			break
		}
	}
	if !exceptionFound {
		for length := count; length >= 1; length-- {
			candidate := strings.Join(labels[count-length:], ".")
			if _, present := l.rules[candidate]; present {
				suffix = length
				break
			}
			if length >= 2 {
				rest := strings.Join(labels[count-length+1:], ".")
				if _, present := l.wildcards[rest]; present {
					suffix = length
					break
				}
			}
		}
		if suffix == 0 {
			// No rule matched: the prevailing rule is "*", a suffix of one label.
			suffix = 1
		}
	}
	if count <= suffix {
		return "", false
	}
	domain := strings.Join(original[count-suffix-1:], ".")
	if trailingDot {
		domain += "."
	}
	return domain, true
}

// canonicalName lower-cases a rule and puts every internationalised label in its
// Punycode form.
func canonicalName(name string) (string, bool) {
	labels := strings.Split(strings.ToLower(name), ".")
	for index, label := range labels {
		if label == "" {
			return "", false
		}
		if label == "*" {
			continue
		}
		ascii, ok := toASCII(label)
		if !ok {
			return "", false
		}
		labels[index] = ascii
	}
	return strings.Join(labels, "."), true
}

// toASCII returns a label in its ASCII form: unchanged when it is ASCII, and
// "xn--" followed by its Punycode encoding otherwise.
func toASCII(label string) (string, bool) {
	ascii := true
	for index := 0; index < len(label); index++ {
		if label[index] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return label, true
	}
	encoded, ok := punycodeEncode([]rune(label))
	if !ok {
		return "", false
	}
	return "xn--" + encoded, true
}

// The Punycode parameters, RFC 3492 section 5.
const (
	punyBase        = 36
	punyTMin        = 1
	punyTMax        = 26
	punySkew        = 38
	punyDamp        = 700
	punyInitialBias = 72
	punyInitialN    = 128
)

// punycodeEncode is the encoding procedure of RFC 3492, section 6.3.
func punycodeEncode(input []rune) (string, bool) {
	var output strings.Builder
	for _, r := range input {
		if r < 0x80 {
			output.WriteRune(r)
		}
	}
	basic := output.Len()
	handled := basic
	if basic > 0 {
		output.WriteByte('-')
	}
	n := punyInitialN
	delta := 0
	bias := punyInitialBias
	for handled < len(input) {
		next := int(^uint(0) >> 1)
		for _, r := range input {
			if int(r) >= n && int(r) < next {
				next = int(r)
			}
		}
		if (next - n) > (int(^uint(0)>>1)-delta)/(handled+1) {
			return "", false
		}
		delta += (next - n) * (handled + 1)
		n = next
		for _, r := range input {
			if int(r) < n {
				delta++
			}
			if int(r) != n {
				continue
			}
			q := delta
			for k := punyBase; ; k += punyBase {
				t := k - bias
				if t < punyTMin {
					t = punyTMin
				} else if t > punyTMax {
					t = punyTMax
				}
				if q < t {
					break
				}
				output.WriteByte(punyDigit(t + (q-t)%(punyBase-t)))
				q = (q - t) / (punyBase - t)
			}
			output.WriteByte(punyDigit(q))
			bias = punyAdapt(delta, handled+1, handled == basic)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return output.String(), true
}

// punyDigit is the basic code point of a digit value, RFC 3492 section 5.
func punyDigit(value int) byte {
	if value < 26 {
		return byte('a' + value)
	}
	return byte('0' + value - 26)
}

// punyAdapt is the bias adaptation function, RFC 3492 section 6.1.
func punyAdapt(delta, points int, first bool) int {
	if first {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / points
	k := 0
	for delta > ((punyBase-punyTMin)*punyTMax)/2 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + (punyBase-punyTMin+1)*delta/(delta+punySkew)
}
