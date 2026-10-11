package plugin

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The adapters of the two service kinds. A service plugin reads no firewall endpoint, so the probe
// its result names is a word of its own -- dataset_current, list_not_modified -- which the
// availability row records as it is, as the compiled MaxMind and Public Suffix List refreshes did.

// serviceResult converts a service answer's result: its probe is a snake_case word.
func serviceResult(checked map[string]any) (provider.Result, error) {
	result := provider.Result{
		State:     store.AvailabilityState(text(checked, "state")),
		Outcome:   store.AvailabilityOutcome(text(checked, "outcome")),
		Detail:    text(checked, "detail"),
		Separable: flag(checked, "separable", false),
	}
	if result.Outcome != "" && !result.Outcome.Valid() {
		return provider.Result{}, fmt.Errorf("result.outcome %q is not one of the closed outcome codes", result.Outcome)
	}
	probe := text(checked, "probe")
	if !namePattern.MatchString(probe) {
		return provider.Result{}, fmt.Errorf("result.probe %q is not a snake_case word", probe)
	}
	result.Probe = opnsense.Endpoint{Path: probe}
	return result, nil
}

// runService calls one function of a service kind and checks its answer.
func (a adapter) runService(ctx context.Context, session provider.Session, function, table string,
	arguments ...any) (map[string]any, error, *failure) {
	answer, _, failed := a.invoker.invoke(ctx, session, function, arguments...)
	if failed != nil {
		return nil, nil, failed
	}
	object, isObject := answer.(map[string]any)
	if !isObject {
		return nil, nil, &failure{code: failureRecords, detail: fmt.Sprintf("%s returned no object", function)}
	}
	answerErr, _ := object["error"].(error)
	delete(object, "error")
	checked, err := checkRecord(a.contract, table, object, function)
	if err != nil {
		return nil, nil, &failure{code: failureRecords, detail: err.Error()}
	}
	return checked, answerErr, nil
}

// ipGeolocationAdapter is a plugin as an ip_geolocation source.
type ipGeolocationAdapter struct{ adapter }

func (a ipGeolocationAdapter) dataset(ctx context.Context, session provider.Session, function string) (
	ipgeolocation.Dataset, provider.Result, error) {
	checked, answerErr, failed := a.runService(ctx, session, function, "dataset_answer")
	if failed != nil {
		return ipgeolocation.Dataset{}, a.failed(failed), failed
	}
	result, err := serviceResult(checked["result"].(map[string]any))
	if err != nil {
		invalid := &failure{code: failureRecords, detail: err.Error()}
		return ipgeolocation.Dataset{}, a.failed(invalid), invalid
	}
	dataset := ipgeolocation.Dataset{Ready: flag(checked, "ready", false), Build: integer(checked, "build")}
	return dataset, result, answerErr
}

func (a ipGeolocationAdapter) Status(ctx context.Context, session provider.Session) (ipgeolocation.Dataset,
	provider.Result, error) {
	return a.dataset(ctx, session, "status")
}

func (a ipGeolocationAdapter) Refresh(ctx context.Context, session provider.Session) (ipgeolocation.Dataset,
	provider.Result, error) {
	return a.dataset(ctx, session, "refresh")
}

func (a ipGeolocationAdapter) Lookup(ctx context.Context, session provider.Session, addresses []string) (
	int64, []ipgeolocation.Answer, error) {
	list := make([]any, 0, len(addresses))
	for _, address := range addresses {
		list = append(list, address)
	}
	checked, answerErr, failed := a.runService(ctx, session, "lookup", "lookup_answer", list)
	if failed != nil {
		return 0, nil, failed
	}
	if answerErr != nil {
		return 0, nil, answerErr
	}
	var answers []ipgeolocation.Answer
	for _, record := range records(checked, "answers") {
		answer := ipgeolocation.Answer{Address: text(record, "address"), CountryCode: textPointer(record, "country_code"),
			CountryName: textPointer(record, "country_name"), ASN: integerPointer(record, "asn"),
			Operator: textPointer(record, "operator")}
		if value, present := record["latitude"].(float64); present {
			answer.Latitude = &value
		}
		if value, present := record["longitude"].(float64); present {
			answer.Longitude = &value
		}
		answers = append(answers, answer)
	}
	return integer(checked, "build"), answers, nil
}

// registrableDomainAdapter is a plugin as a registrable_domain source.
type registrableDomainAdapter struct{ adapter }

func (a registrableDomainAdapter) list(ctx context.Context, session provider.Session, function string) (
	registrabledomain.List, provider.Result, error) {
	checked, answerErr, failed := a.runService(ctx, session, function, "list_answer")
	if failed != nil {
		return registrabledomain.List{}, a.failed(failed), failed
	}
	result, err := serviceResult(checked["result"].(map[string]any))
	if err != nil {
		invalid := &failure{code: failureRecords, detail: err.Error()}
		return registrabledomain.List{}, a.failed(invalid), invalid
	}
	list := registrabledomain.List{Ready: flag(checked, "ready", false), Version: text(checked, "version"),
		Rules: int(integer(checked, "rules")), ConfirmedAt: integer(checked, "confirmed_at"),
		Failed: flag(checked, "failed", false)}
	return list, result, answerErr
}

func (a registrableDomainAdapter) Status(ctx context.Context, session provider.Session) (registrabledomain.List,
	provider.Result, error) {
	return a.list(ctx, session, "status")
}

func (a registrableDomainAdapter) Refresh(ctx context.Context, session provider.Session) (registrabledomain.List,
	provider.Result, error) {
	return a.list(ctx, session, "refresh")
}

func (a registrableDomainAdapter) Group(ctx context.Context, session provider.Session, names []string) (
	map[string]string, error) {
	list := make([]any, 0, len(names))
	for _, name := range names {
		list = append(list, name)
	}
	checked, answerErr, failed := a.runService(ctx, session, "group", "group_answer", list)
	if failed != nil {
		return nil, failed
	}
	if answerErr != nil {
		return nil, answerErr
	}
	groups := map[string]string{}
	for _, record := range records(checked, "groups") {
		if domain := text(record, "registrable_domain"); domain != "" {
			groups[text(record, "name")] = domain
		}
	}
	return groups, nil
}
