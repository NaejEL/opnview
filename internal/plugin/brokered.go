package plugin

import (
	"slices"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/infer"
)

// What a plugin's manifest tells the broker (A10, A12, A14 and A17 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md): each endpoint's default max age and floor, the
// corrections of what the broker infers of its answers, a stream's discarded events and the polled
// endpoint it replaces. Every key is optional; a manifest stating none loads unchanged.

// checkBrokerKeys checks one endpoint's broker keys.
func checkBrokerKeys(manifest Manifest, declaration EndpointDeclaration) *Refusal {
	invalid := func(format string, arguments ...any) *Refusal {
		return refuse(manifest.Name, RefusalMirrorCorrectionInvalid, "the endpoint %q: "+format,
			append([]any{declaration.Name}, arguments...)...)
	}
	if declaration.MaxAgeSeconds < 0 || declaration.FloorSeconds < 0 || declaration.DiscardEvents < 0 {
		return invalid("max_age_seconds, floor_seconds and discard_events are not negative")
	}
	if declaration.Replaces != "" {
		if !declaration.Stream {
			return invalid("only a stream replaces another endpoint")
		}
		found := false
		for _, other := range manifest.Endpoints {
			found = found || (other.Name == declaration.Replaces && other.Name != declaration.Name)
		}
		if !found {
			return invalid("replaces names %q, which is no other endpoint of the manifest", declaration.Replaces)
		}
	}
	corrections := declaration.Corrections
	if corrections == nil {
		return nil
	}
	if corrections.Nature != "" && corrections.Nature != infer.NatureSnapshot &&
		corrections.Nature != infer.NatureRecordStream {
		return invalid("the nature %q is not snapshot or record_stream", corrections.Nature)
	}
	for name, role := range corrections.Parameters {
		if strings.TrimSpace(name) == "" || !slices.Contains(infer.Roles(), role) {
			return invalid("the parameter %q has the role %q, not one of %s", name, role,
				strings.Join(infer.Roles(), ", "))
		}
	}
	for path, kind := range corrections.Types {
		if !wellFormedPath(path) || !slices.Contains(infer.Types(), kind) {
			return invalid("the field %q has the type %q, not one of %s", path, kind, strings.Join(infer.Types(), ", "))
		}
	}
	for path := range corrections.Domains {
		if !wellFormedPath(path) {
			return invalid("the field path %q is malformed", path)
		}
	}
	for _, path := range corrections.ValueKeyed {
		if !wellFormedPath(path) {
			return invalid("the field path %q is malformed", path)
		}
	}
	if corrections.RowSet != "" && !wellFormedPath(corrections.RowSet) {
		return invalid("the row set %q is malformed", corrections.RowSet)
	}
	for _, name := range append(append([]string(nil), corrections.Key...), corrections.Cursor...) {
		if strings.TrimSpace(name) == "" {
			return invalid("a key or a cursor names an empty field")
		}
	}
	return nil
}

// wellFormedPath reports whether a field path parses into non-empty steps.
func wellFormedPath(path string) bool {
	steps := answer.Steps(path)
	if len(steps) == 0 {
		return false
	}
	for _, step := range steps {
		if step == "" {
			return false
		}
	}
	return answer.Join(joinSteps(steps[:len(steps)-1]), steps[len(steps)-1]) != "" || len(steps) > 0
}

func joinSteps(steps []string) string {
	path := ""
	for _, step := range steps {
		path = answer.Join(path, step)
	}
	return path
}

// declareTo tells a broker what the plugin declares of its endpoints, and records the endpoints
// whose corrections lost to another plugin's.
func (p *Plugin) declareTo(gate *broker.Broker) {
	if gate == nil {
		return
	}
	for _, declaration := range p.manifest.Endpoints {
		endpoint, found := p.endpoints[declaration.Name]
		if !found {
			continue
		}
		brokered := broker.EndpointDeclaration{
			MaxAge:        time.Duration(declaration.MaxAgeSeconds * float64(time.Second)),
			Floor:         time.Duration(declaration.FloorSeconds * float64(time.Second)),
			Discard:       declaration.DiscardEvents,
			UpstreamURL:   endpoint.UpstreamURL,
			SurveySection: endpoint.SurveySection,
		}
		if declaration.Stream {
			flag := true
			brokered.Stream = &flag
		}
		if declaration.Corrections != nil {
			brokered.Corrections = *declaration.Corrections
		}
		if declaration.Replaces != "" {
			if replaced, found := p.endpoints[declaration.Replaces]; found {
				brokered.Replaces = replaced.Path
			}
		}
		if code := gate.Declare(p.Name, p.Origin == OriginBuiltin, endpoint, brokered); code != "" {
			p.CorrectionConflicts = append(p.CorrectionConflicts, CorrectionConflict{Endpoint: declaration.Name,
				Code: code})
		}
		// Each versioned route is an endpoint of its own to the broker: a different path is a
		// different datum, with the route's own corrections.
		routes := p.routes[declaration.Name]
		for index, variant := range declaration.Variants {
			if index+1 >= len(routes) {
				break
			}
			variantDeclared := brokered
			variantDeclared.UpstreamURL, variantDeclared.SurveySection = variant.UpstreamURL, variant.SurveySection
			variantDeclared.Corrections = infer.Corrections{}
			if variant.Corrections != nil {
				variantDeclared.Corrections = *variant.Corrections
			}
			if code := gate.Declare(p.Name, p.Origin == OriginBuiltin, routes[index+1].endpoint, variantDeclared); code != "" {
				p.CorrectionConflicts = append(p.CorrectionConflicts, CorrectionConflict{Endpoint: declaration.Name,
					Code: code})
			}
		}
	}
}

// CorrectionConflict is an endpoint whose correction lost to another plugin's: the plugin loads,
// and is listed with the code.
type CorrectionConflict struct {
	Endpoint string `json:"endpoint"`
	Code     string `json:"code"`
}
