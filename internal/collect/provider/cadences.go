package provider

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A core kind's cadences, page size and sizing, as its kind file declares them (PA2 of the plugin-API
// amendment to specs/SPEC-backend-completion-plugins.md): the intervals the core runs its passes at, the
// page its paged read asks for, and how the collection surface measures that read. Each is a setting an
// installation may override, under the key the file names; the default and the reason for it sit beside
// the key, so the justification cannot drift from the number. Adding a kind with a cadence is adding its
// file: the scheduler, the configuration and the collection surface read these, and no list names a kind.

// The cadence roles a kind file names. A firewall source has one, its poll; a service kind has a refresh of
// what it acquires and, for the geolocation, a lookup of what it places.
const (
	// CadencePoll is the interval of a kind's collection pass.
	CadencePoll = "poll"
	// CadenceRefresh is how often a service kind's provider checks for a newer copy of what it acquired.
	CadenceRefresh = "refresh"
	// CadenceLookup is how often a service kind's provider is asked about what the core stored since.
	CadenceLookup = "lookup"
)

// Cadence is one interval of a kind.
type Cadence struct {
	// Role is poll, refresh or lookup.
	Role string `json:"role"`
	// Setting is the setting key an installation overrides it under.
	Setting string `json:"setting"`
	// DefaultSeconds is the interval with no setting row.
	DefaultSeconds int64 `json:"default_seconds"`
	// Basis is why the default is what it is: the survey section, or the decision, behind it.
	Basis string `json:"basis"`
}

// Default is the cadence's interval with no setting row.
func (c Cadence) Default() time.Duration { return time.Duration(c.DefaultSeconds) * time.Second }

// PageSize is the setting sizing a kind's paged read.
type PageSize struct {
	// Setting is the setting key.
	Setting string `json:"setting"`
	// Default is the page with no setting row: within what the firewall serves for the read.
	Default int `json:"default"`
	// Basis is why the default is what it is.
	Basis string `json:"basis"`
}

// Sizing is how the collection surface measures a kind's read and suggests the pair it polls at.
type Sizing struct {
	// Order is the kind's place among the sized kinds on the collection surface.
	Order int `json:"order"`
	// Table holds the kind's records, SourceColumn the instant the source gave each and IngestedColumn
	// the instant opnview stored it, empty when the table records only one.
	Table          string `json:"table"`
	SourceColumn   string `json:"source_column"`
	IngestedColumn string `json:"ingested_column"`
	// PageCeiling is the largest page the firewall serves for the read, 0 when nothing caps it.
	PageCeiling int `json:"page_ceiling"`
	// SurveyHeading is the heading of the survey section recording the ceiling, verbatim, so a test can
	// hold the citation to docs/opnsense-api-survey.md.
	SurveyHeading string `json:"survey_heading"`
	// Basis is what the ceiling is.
	Basis string `json:"basis"`
}

// CollectionPlacement places a polled kind's card on the collection surface.
type CollectionPlacement struct {
	// After are the cards it follows, the first one drawn; none drawn puts it last.
	After []string `json:"after"`
}

// CadenceOf returns one of a kind's cadences by role.
func (c Contract) CadenceOf(role string) (Cadence, bool) {
	for _, cadence := range c.Cadences {
		if cadence.Role == role {
			return cadence, true
		}
	}
	return Cadence{}, false
}

// settingKeyPattern is what a setting key a kind file names matches: a snake_case word.
var settingKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// checkSettings reports what is wrong with a kind file's cadences, page size, sizing and placement,
// empty when nothing is.
func checkSettings(contract Contract) string {
	roles := map[string]bool{}
	for _, cadence := range contract.Cadences {
		switch cadence.Role {
		case CadencePoll, CadenceRefresh, CadenceLookup:
		default:
			return fmt.Sprintf("the cadence role %q is not poll, refresh or lookup", cadence.Role)
		}
		if roles[cadence.Role] {
			return fmt.Sprintf("the cadence %s is declared twice", cadence.Role)
		}
		roles[cadence.Role] = true
		if !settingKeyPattern.MatchString(cadence.Setting) || cadence.DefaultSeconds <= 0 ||
			strings.TrimSpace(cadence.Basis) == "" {
			return fmt.Sprintf("the cadence %s needs a snake_case setting, a positive default and its basis",
				cadence.Role)
		}
	}
	if contract.OwnCadence && len(contract.Cadences) > 0 {
		return "a kind whose providers state their own cadence declares none"
	}
	if page := contract.PageSize; page != nil {
		if !settingKeyPattern.MatchString(page.Setting) || page.Default <= 0 || strings.TrimSpace(page.Basis) == "" {
			return "the page size needs a snake_case setting, a positive default and its basis"
		}
		if contract.Sizing != nil && contract.Sizing.PageCeiling > 0 && page.Default > contract.Sizing.PageCeiling {
			return "the default page is above the ceiling the firewall serves"
		}
	}
	if sizing := contract.Sizing; sizing != nil {
		if _, polled := contract.CadenceOf(CadencePoll); !polled {
			return "a sized kind is polled: its poll cadence is the half of the pair the suggestion moves"
		}
		if sizing.Order <= 0 || !settingKeyPattern.MatchString(sizing.Table) ||
			!settingKeyPattern.MatchString(sizing.SourceColumn) ||
			(sizing.IngestedColumn != "" && !settingKeyPattern.MatchString(sizing.IngestedColumn)) ||
			sizing.PageCeiling < 0 || strings.TrimSpace(sizing.SurveyHeading) == "" ||
			strings.TrimSpace(sizing.Basis) == "" {
			return "the sizing needs an order, a table and its columns, a ceiling, its survey heading and its basis"
		}
	}
	if placement := contract.Collection; placement != nil {
		if contract.Sizing != nil {
			return "a sized kind's card is placed by its sizing order, not after another card"
		}
		if _, polled := contract.CadenceOf(CadencePoll); !polled || len(placement.After) == 0 {
			return "a placed card is a polled kind's, after at least one other card"
		}
	}
	return ""
}

// checkSettingsAcross reports what is wrong across the kind files: a setting key two cadences or pages
// share, or two sized kinds in one place. Empty when nothing is.
func checkSettingsAcross(contracts []Contract) string {
	keys := map[string]string{}
	orders := map[int]string{}
	claim := func(key, kind string) string {
		if owner, taken := keys[key]; taken {
			return fmt.Sprintf("the setting %s is declared by %s and %s", key, owner, kind)
		}
		keys[key] = kind
		return ""
	}
	for _, contract := range contracts {
		for _, cadence := range contract.Cadences {
			if problem := claim(cadence.Setting, contract.Kind); problem != "" {
				return problem
			}
		}
		if contract.PageSize != nil {
			if problem := claim(contract.PageSize.Setting, contract.Kind); problem != "" {
				return problem
			}
		}
		if contract.Sizing != nil {
			if owner, taken := orders[contract.Sizing.Order]; taken {
				return fmt.Sprintf("the sized kinds %s and %s share the order %d", owner, contract.Kind,
					contract.Sizing.Order)
			}
			orders[contract.Sizing.Order] = contract.Kind
		}
	}
	return ""
}

// KindCadence is one cadence of one kind.
type KindCadence struct {
	Kind string
	Cadence
}

// Cadences returns every core kind's cadences, by kind and in each file's order.
func Cadences() []KindCadence {
	var all []KindCadence
	for _, contract := range Kinds() {
		for _, cadence := range contract.Cadences {
			all = append(all, KindCadence{Kind: contract.Kind, Cadence: cadence})
		}
	}
	return all
}

// CadenceOfKind returns a core kind's cadence of a role.
func CadenceOfKind(kind, role string) (Cadence, bool) {
	contract, found := KindOf(kind)
	if !found {
		return Cadence{}, false
	}
	return contract.CadenceOf(role)
}

// KindPageSize is one kind's page-size setting.
type KindPageSize struct {
	Kind string
	PageSize
}

// PageSizes returns every core kind's page-size setting, by kind.
func PageSizes() []KindPageSize {
	var all []KindPageSize
	for _, contract := range Kinds() {
		if contract.PageSize != nil {
			all = append(all, KindPageSize{Kind: contract.Kind, PageSize: *contract.PageSize})
		}
	}
	return all
}

// SizedKinds returns the kinds the collection surface sizes, in their order.
func SizedKinds() []Contract {
	var sized []Contract
	for _, contract := range Kinds() {
		if contract.Sizing != nil {
			sized = append(sized, contract)
		}
	}
	sort.SliceStable(sized, func(i, j int) bool { return sized[i].Sizing.Order < sized[j].Sizing.Order })
	return sized
}

// PolledKinds returns the kinds the collection surface draws a card for that it does not size: those
// placed after another card, by kind.
func PolledKinds() []Contract {
	var polled []Contract
	for _, contract := range Kinds() {
		if contract.Collection != nil {
			polled = append(polled, contract)
		}
	}
	return polled
}
