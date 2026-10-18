package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// A plugin as an implementation of a kind (PA14 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md): one function called by name -- by its script, or by its
// declarative form -- its answer checked once against the kind's contract (provider/kinds), and the
// checked record handed to the kind's coordinator as it is. There is no adapter per kind: what a kind
// makes of a record is its coordinator's, in internal/collect.

// invoker is a plugin's way of answering a function of a kind: a script, or a declarative form.
type invoker interface {
	invoke(ctx context.Context, session provider.Session, function string, arguments ...any) (
		answer any, hostErrors []error, failed *scripthost.Failure)
}

// scriptInvoker calls a script's exported functions under one kind's namespace.
type scriptInvoker struct {
	script    *scripthost.Script
	namespace string
}

func (i scriptInvoker) invoke(ctx context.Context, session provider.Session, function string,
	arguments ...any) (any, []error, *scripthost.Failure) {
	return i.script.Call(ctx, session, i.namespace, function, arguments...)
}

// implementation is one provider of a plugin.
type implementation struct {
	plugin    string
	contract  provider.Contract
	endpoints map[string]opnsense.Endpoint
	invoker   invoker
	// scriptProbe names the plugin in an availability row when no endpoint decided the outcome: a
	// script that threw or ran too long.
	scriptProbe opnsense.Endpoint
	// tables are the plugin's own tables, for a kind it declared: a collect answer's rows are checked
	// against them.
	tables map[string]plugindecl.PluginTable
	// pushFed is the plugin when the kind's records reach this provider only by push, so its availability is
	// the polled read's and its push entries' together (withPushAvailability); key is the provider's key.
	pushFed *Plugin
	key     string
}

// Call runs one function of the kind and checks its answer against the table the function returns.
func (i implementation) Call(ctx context.Context, session provider.Session, function string,
	arguments ...any) (provider.Answer, provider.Result, error) {
	declared, found := i.contract.FunctionOf(function)
	if !found {
		invalid := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("the %s contract has no "+
			"function %s", i.contract.Kind, function)}
		return nil, i.failed(invalid), invalid
	}
	answer, result, err := i.call(ctx, session, function, declared.Returns, arguments...)
	if i.pushFed != nil {
		result = i.pushFed.withPushAvailability(i.contract.Kind, i.key, result)
	}
	return answer, result, err
}

// call runs one function and checks its answer against a table. It returns the checked answer, the
// result, and the error the kind should see: the failure of the call, the answer's own error, or
// provider.ErrUnsupportedRead with the answer's reason.
func (i implementation) call(ctx context.Context, session provider.Session, function, table string,
	arguments ...any) (provider.Answer, provider.Result, error) {
	answer, _, failed := i.invoker.invoke(ctx, session, function, arguments...)
	if failed != nil {
		return nil, i.failed(failed), failed
	}
	object, isObject := answer.(map[string]any)
	if !isObject {
		return i.invalid(fmt.Sprintf("%s returned no object", function))
	}
	var own map[string]any
	if i.tables != nil && function == "collect" {
		// A declared kind's rows are checked against the plugin's own tables, its state as text.
		rows, err := scripthost.CheckRows(i.tables, object["rows"])
		if err != nil {
			return i.invalid(err.Error())
		}
		state, err := scripthost.CheckState(object["state"])
		if err != nil {
			return i.invalid(err.Error())
		}
		own = map[string]any{"rows": rows, "state": state}
		delete(object, "rows")
		delete(object, "state")
	}
	answerErr, _ := object["error"].(error)
	delete(object, "error")
	checked, err := scripthost.CheckRecord(i.contract, table, object, function)
	if err != nil {
		return i.invalid(err.Error())
	}
	var result provider.Result
	if raw, carries := checked["result"].(map[string]any); carries {
		if i.contract.Service {
			result, err = serviceResult(raw)
		} else {
			result, err = scripthost.ResultOf(raw, i.endpoints)
		}
		if err != nil {
			return i.invalid(err.Error())
		}
	}
	for name, value := range own {
		checked[name] = value
	}
	if answerErr != nil {
		return checked, result, answerErr
	}
	if unsupported := provider.Text(checked, "unsupported"); unsupported != "" {
		return checked, result, fmt.Errorf("%w: %s", provider.ErrUnsupportedRead, unsupported)
	}
	return checked, result, nil
}

// failed is the result a failed call is recorded with.
func (i implementation) failed(failed *scripthost.Failure) provider.Result {
	return provider.Result{State: vocab.StateUnavailable, Outcome: failed.Code, Probe: i.scriptProbe,
		Detail: fmt.Sprintf("the plugin %s: %s", i.plugin, failed.Detail)}
}

// invalid answers a call whose answer the contract refuses.
func (i implementation) invalid(detail string) (provider.Answer, provider.Result, error) {
	failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: detail}
	return nil, i.failed(failed), failed
}

// serviceResult converts a service answer's result. A service plugin reads no firewall endpoint, so the
// probe its result names is a word of its own -- dataset_current, list_not_modified -- which the
// availability row records as it is.
func serviceResult(checked map[string]any) (provider.Result, error) {
	result := provider.Result{
		State:     vocab.AvailabilityState(provider.Text(checked, "state")),
		Outcome:   vocab.AvailabilityOutcome(provider.Text(checked, "outcome")),
		Detail:    provider.Text(checked, "detail"),
		Separable: provider.Flag(checked, "separable", false),
	}
	if result.Outcome != "" && !result.Outcome.Valid() {
		return provider.Result{}, fmt.Errorf("result.outcome %q is not one of the closed outcome codes", result.Outcome)
	}
	probe := provider.Text(checked, "probe")
	if !contract.NamePattern.MatchString(probe) {
		return provider.Result{}, fmt.Errorf("result.probe %q is not a snake_case word", probe)
	}
	result.Probe = opnsense.Endpoint{Path: probe}
	return result, nil
}

// liveImplementation is an implementation declaring a live pass.
type liveImplementation struct {
	implementation
	script *scripthost.Script
	live   *liveBinding
}

// LiveCadence names the cadence setting, its default, its lower bound and its endpoints' floor.
func (i liveImplementation) LiveCadence() (string, int64, int64, int64) {
	return i.live.settingKey, i.live.fallback, i.live.minimum, i.live.floor
}

// CallLive runs the live pass, its requests accepting an answer at most maxAge old and reading only its
// live endpoints.
func (i liveImplementation) CallLive(ctx context.Context, session provider.Session, maxAge time.Duration,
	arguments ...any) (provider.Answer, provider.Result, error) {
	live := i.implementation
	live.invoker = liveInvoker{script: i.script, namespace: i.contract.Kind, maxAge: maxAge, only: i.live.only}
	return live.call(ctx, session, i.live.function, i.contract.LiveAnswer, arguments...)
}

// declaredContract is the contract of a declared kind: probe and collect, whose answer holds the
// rows of the plugin's tables and its durable state.
func declaredContract(kind string) provider.Contract {
	return provider.Contract{
		Kind: kind,
		Functions: []provider.Function{
			{Name: "probe", Arguments: "host", Returns: "probe_answer"},
			{Name: "collect", Arguments: "host, state", Returns: "collect_answer"},
		},
		Records: []provider.Record{
			{Name: "probe_answer", Fields: []provider.Field{
				{Name: "result", Type: provider.FieldRecord, Of: "result", Required: true},
				{Name: "error", Type: provider.FieldText},
			}},
			{Name: "collect_answer", Fields: []provider.Field{
				{Name: "result", Type: provider.FieldRecord, Of: "result", Required: true},
				{Name: "rows", Type: provider.FieldObject},
				{Name: "state", Type: provider.FieldObject},
				{Name: "error", Type: provider.FieldText},
				{Name: "unsupported", Type: provider.FieldText},
			}},
		},
	}
}

// exportedValues reads the values a kind's contract names from the plugin's script, each checked as
// its field; a declarative provider, which exports none, takes each value's declared default.
func (p *Plugin) exportedValues(kindContract provider.Contract, declarative bool) (map[string]any, error) {
	if len(kindContract.Values) == 0 {
		return nil, nil
	}
	raw := map[string]any{}
	for _, field := range kindContract.Values {
		if declarative || p.script == nil {
			if field.Default != nil {
				raw[field.Name] = field.Default
			}
			continue
		}
		value, err := p.script.ExportedValue(kindContract.Kind, field.Name)
		if err != nil {
			if !field.Required {
				continue
			}
			return nil, err
		}
		raw[field.Name] = value
	}
	values := provider.Contract{Kind: kindContract.Kind,
		Records: []provider.Record{{Name: "values", Fields: kindContract.Values}}}
	checked, err := scripthost.CheckRecord(values, "values", raw, kindContract.Kind)
	if err != nil {
		return nil, err
	}
	return checked, nil
}

// scriptEndpoint is the probe an availability row names when the plugin itself, and no endpoint,
// decided the outcome.
func scriptEndpoint(plugin, file string) opnsense.Endpoint {
	if file == "" {
		file = contract.ManifestFile
	}
	return opnsense.Endpoint{Method: "plugin", Path: plugin + "/" + file}
}

// The OPNsense pages that turn an installed provider on (docs/widget-catalogue.md, Source availability:
// present_but_disabled reads "installed, not collecting" WITH THE OPNSENSE PAGE THAT TURNS IT ON, and Part
// 3 adds that the page is always given with the statement that opnview never performs that change).
//
// EACH PROVIDER DECLARES ITS OWN, in its manifest (contract.EnablePage): a path on the firewall, the
// catalogue key naming the page, and the menu definition at the OPNsense version the survey pins that
// establishes the path -- a built-in carries it, which a test holds to the pinned version; a third party is
// asked to, as for an endpoint's upstream_url. No path is guessed. The host is never written: the Sources surface links the
// firewall base URL the operator entered followed by the path, and with no URL entered draws no link. A
// provider declaring none gets no link, which is how a provider no OPNsense setting makes collect says so.

// checkEnablePages validates the pages a manifest's providers declare.
func checkEnablePages(manifest contract.Manifest) *contract.Refusal {
	namespace := "plugin." + manifest.Name + "."
	for _, declaration := range manifest.Providers {
		for _, page := range declaration.EnablePages {
			switch {
			case !strings.HasPrefix(page.Path, "/ui/") || strings.ContainsAny(page.Path, " \"<>"):
				return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "the %s provider's enable page %q is "+
					"not a path of the firewall's web interface, /ui/...", declaration.Kind, page.Path)
			case !strings.HasPrefix(page.Label, namespace):
				return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "the %s provider's enable page %s names "+
					"its label by a key of the plugin's own catalogue, %s*", declaration.Kind, page.Path, namespace)
			}
		}
	}
	return nil
}

// EnablePages returns the OPNsense pages that turn one registry row's provider on, as its plugin declares
// them. None when nothing declares one: a core kind's registered row (flow_volume's) has no implementation,
// so nothing ever finds it installed and not collecting.
func (l *Loaded) EnablePages(kind, providerKey string) []provider.EnablePage {
	if l != nil {
		for _, member := range l.Members {
			for _, info := range member.Providers {
				if info.Kind != kind || info.Key != providerKey {
					continue
				}
				for _, declaration := range member.Manifest.Providers {
					key := declaration.Key
					if key == "" {
						key = member.Name
					}
					if declaration.Kind == kind && key == providerKey {
						return append([]provider.EnablePage(nil), declaration.EnablePages...)
					}
				}
			}
		}
	}
	return nil
}
