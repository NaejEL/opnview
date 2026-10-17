// Command answerdrift answers every served widget type again on a seeded database and fails when a
// type's member paths differ from the ones the front's recorded answers carry (A9 of
// specs/SPEC-step-7-frontend.md).
//
// The front's renderers are tested on recorded answers (internal/front/frontjs/testdata/
// widget_answers.json.gz), recorded from the widget API over the widget package's small fixture. A
// renderer drawn on an answer the API no longer gives passes its tests and fails in a browser, so the
// pre-deployment command (ci/pre-deployment.sh) runs this program on the 100 000-row seed the schema
// checks keep: every served type is answered at its defaults, as the canvas first asks it, and its
// member paths -- every object member, an array's elements merged as "[]" -- are compared with the
// union of the member paths of that type's recorded answers.
//
//   - A path the seed's answer carries that no recorded answer carries is a member no renderer test
//     has seen: drift.
//   - A path every object at its place carries in a recorded answer at the defaults, that the seed's
//     answer lacks where it holds such objects, is a member the renderers read and the API no longer
//     gives: drift. A member only some elements of an array carry (an address row beside the site
//     rows), a path below an array the seed left empty, and one below a member the seed answered null
//     are data the seed does not have, never drift.
//
// THE SEED'S ANSWERS ARE RECORDED TOO. The small fixture cannot give every member a seed of 100 000
// flows gives -- collection gaps, the NetFlow coverage states, an address history -- so the recorded
// answers hold, beside the fixture's, the seed's own answer of every type at its defaults, keyed
// "<type> seed:100000", and the renderers are drawn on both. With -record, the program writes the
// seed's answers it just made into the file it read, replacing the seed's entries and keeping the
// fixture's; ci/pre-deployment.sh does so when OPNVIEW_RECORD_SEED_ANSWERS=1 is set. A run that fails
// names each drifted path; the fix is to record the answers again -- the fixture's with the generator
// named in internal/front/frontjs/answers_test.go, the seed's with -record -- and to make the
// renderers draw what changed.
//
//	go run ./ci/answerdrift -database '<connection string>' -now <epoch> -answers <widget_answers.json.gz> [-record]
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/widget"
	_ "github.com/NaejEL/opnview/internal/widget/widgets"
)

func main() {
	database := flag.String("database", "", "the PostgreSQL connection string of the seeded database")
	now := flag.Int64("now", 0, "the seed's instant, in UTC epoch seconds")
	answers := flag.String("answers", "", "the front's recorded answers, gzipped JSON keyed \"<type> <parameters>\"")
	record := flag.Bool("record", false, "write the seed's answers into the recorded answers instead of comparing")
	flag.Parse()
	if *database == "" || *now == 0 || *answers == "" {
		fmt.Fprintln(os.Stderr, "answerdrift: -database, -now and -answers are required")
		os.Exit(2)
	}
	drifted, err := run(*database, *now, *answers, *record)
	if err != nil {
		fmt.Fprintln(os.Stderr, "answerdrift:", err)
		os.Exit(1)
	}
	if drifted > 0 {
		fmt.Fprintf(os.Stderr, "answerdrift: %d member paths differ from the recorded answers\n", drifted)
		os.Exit(1)
	}
}

// recorded is one type's recorded answers: the union of their member paths, and the paths every
// object at their place carries in the ones asked at the defaults.
type recorded struct {
	all      map[string]bool
	defaults map[string]bool
}

// SeedKey is the parameters part of the key a seed's answer is recorded under.
const SeedKey = "seed:100000"

func run(dsn string, now int64, answersFile string, record bool) (int, error) {
	byType, entries, err := readRecorded(answersFile)
	if err != nil {
		return 0, err
	}
	ctx := context.Background()
	database, err := store.Open(ctx, dsn)
	if err != nil {
		return 0, err
	}
	defer func() { _ = database.Close() }()
	service, err := widget.New(database, widget.Options{Now: func() time.Time { return time.Unix(now, 0) }})
	if err != nil {
		return 0, err
	}
	client, err := anyClient(ctx, database)
	if err != nil {
		return 0, err
	}
	drifted := 0
	types := service.Types()
	sort.Strings(types)
	for _, widgetType := range types {
		parameters := map[string]any{}
		if widgetType == "client_traffic_detail" {
			parameters["client"] = client
		}
		encoded, _ := json.Marshal(parameters)
		response, err := service.Respond(ctx, widgetType, string(encoded))
		if err != nil {
			return drifted, fmt.Errorf("%s: %w", widgetType, err)
		}
		raw, err := json.Marshal(response)
		if err != nil {
			return drifted, fmt.Errorf("%s: %w", widgetType, err)
		}
		var answer any
		if err := json.Unmarshal(raw, &answer); err != nil {
			return drifted, fmt.Errorf("%s: %w", widgetType, err)
		}
		if record {
			entries[widgetType+" "+SeedKey] = raw
			fmt.Printf("  answer-drift %-30s recorded, condition %s\n", widgetType, response.Condition)
			continue
		}
		seen := newPathSet()
		seen.walk(answer, "")
		known, found := byType[widgetType]
		if !found {
			fmt.Printf("  answer-drift %-30s DRIFT: no recorded answer\n", widgetType)
			drifted++
			continue
		}
		added, removed := compare(seen, known)
		verdict := "ok"
		if len(added)+len(removed) > 0 {
			verdict = "DRIFT"
		}
		fmt.Printf("  answer-drift %-30s %-5s condition %s, %d member paths\n", widgetType, verdict,
			response.Condition, len(seen.paths))
		for _, path := range added {
			fmt.Printf("    answered on the seed, in no recorded answer: %s\n", path)
		}
		for _, path := range removed {
			fmt.Printf("    in the recorded answer, not answered on the seed: %s\n", path)
		}
		drifted += len(added) + len(removed)
	}
	if record {
		return 0, writeRecorded(answersFile, entries)
	}
	for widgetType := range byType {
		if _, served := service.Preset(widgetType); !served {
			fmt.Printf("  answer-drift %-30s DRIFT: recorded, no longer served\n", widgetType)
			drifted++
		}
	}
	return drifted, nil
}

// compare answers the paths the seed added and the paths it lost.
func compare(seen pathSet, known recorded) (added, removed []string) {
	for path := range seen.paths {
		if !known.all[path] {
			added = append(added, path)
		}
	}
	for path := range known.defaults {
		if seen.paths[path] {
			continue
		}
		parent := path[:strings.LastIndex(path, ".")]
		// The seed holds the object the member belongs to, and nothing above it is an array it left
		// empty or a member it answered null: the member is gone.
		if seen.objects[parent] && !seen.underEmpty(parent) {
			removed = append(removed, path)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// pathSet is the member paths of one answer: every member, every object's path, the paths whose
// value is an empty array or null, and how many objects sit at a path and carry a member.
type pathSet struct {
	paths   map[string]bool
	objects map[string]bool
	empty   map[string]bool
	counts  map[string]int
	carried map[string]int
}

func newPathSet() pathSet {
	return pathSet{paths: map[string]bool{}, objects: map[string]bool{}, empty: map[string]bool{},
		counts: map[string]int{}, carried: map[string]int{}}
}

// always reports whether every object at a member's place carries it.
func (s pathSet) always(path string) bool {
	parent := path[:strings.LastIndex(path, ".")]
	return s.carried[path] == s.counts[parent]
}

func (s pathSet) walk(value any, path string) {
	switch typed := value.(type) {
	case map[string]any:
		s.objects[path] = true
		s.counts[path]++
		for key, member := range typed {
			s.paths[path+"."+key] = true
			s.carried[path+"."+key]++
			s.walk(member, path+"."+key)
		}
	case []any:
		if len(typed) == 0 {
			s.empty[path] = true
		}
		for _, element := range typed {
			s.walk(element, path+"[]")
		}
	case nil:
		s.empty[path] = true
	}
}

// underEmpty reports whether a path, or one above it, is a value the seed left empty or null.
func (s pathSet) underEmpty(path string) bool {
	for at := path; at != ""; {
		if s.empty[at] {
			return true
		}
		cut := strings.LastIndexAny(at, ".[")
		if cut < 0 {
			return false
		}
		at = at[:cut]
	}
	return false
}

// readRecorded reads the recorded answers, by type, and the file's entries as they are.
func readRecorded(file string) (map[string]recorded, map[string]json.RawMessage, error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = handle.Close() }()
	reader, err := gzip.NewReader(handle)
	if err != nil {
		return nil, nil, err
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, err
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, nil, err
	}
	byType := map[string]recorded{}
	for key, body := range entries {
		widgetType, parameters, _ := strings.Cut(key, " ")
		var answer any
		if err := json.Unmarshal(body, &answer); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		set := newPathSet()
		set.walk(answer, "")
		entry, found := byType[widgetType]
		if !found {
			entry = recorded{all: map[string]bool{}, defaults: map[string]bool{}}
		}
		for path := range set.paths {
			entry.all[path] = true
			if atDefaults(widgetType, parameters) && set.always(path) {
				entry.defaults[path] = true
			}
		}
		byType[widgetType] = entry
	}
	return byType, entries, nil
}

// writeRecorded writes the recorded answers back, gzipped, as the generator writes them.
func writeRecorded(file string, entries map[string]json.RawMessage) error {
	encoded, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(encoded); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return os.WriteFile(file, compressed.Bytes(), 0o644)
}

// atDefaults reports whether a recorded request is the one this program asks the seed: the seed's own
// answer, or the type with no parameter, but client_traffic_detail, which requires a client and is
// asked with one alone.
func atDefaults(widgetType, parameters string) bool {
	if parameters == SeedKey {
		return true
	}
	if widgetType == "client_traffic_detail" {
		var given map[string]any
		if json.Unmarshal([]byte(parameters), &given) != nil {
			return false
		}
		_, client := given["client"]
		return client && len(given) == 1
	}
	return parameters == ""
}

// anyClient is a client of the seed the format can name, for the one type that requires one.
func anyClient(ctx context.Context, database *store.Store) (map[string]any, error) {
	var kind, key string
	err := database.DB().QueryRowContext(ctx, `SELECT identity_kind, identity_key FROM client
		WHERE identity_kind IN ('mac', 'dhcp_client_id') ORDER BY id LIMIT 1`).Scan(&kind, &key)
	if err != nil {
		return nil, fmt.Errorf("finding a client of the seed: %w", err)
	}
	return map[string]any{"kind": "client", "by": kind, "value": key}, nil
}
