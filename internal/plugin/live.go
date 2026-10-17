package plugin

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
)

// The live pass (C3 of the backend-completion cycle, docs/plugins.md "Live pass").
//
// A provider may declare, beside its kind's functions, a live pass: one exported function of the
// kind's namespace that reads only the live endpoints it names, run by the host at the live cadence
// a declared setting holds -- and only while somebody looks at a live figure (internal/live). Its
// requests carry that cadence as their max age, so the broker classes them real-time when the
// cadence is at or below its threshold and coalesces them with every other reader of the same
// datum; the cadence is never served below an endpoint's freshness floor, which the broker applies.
// Its records are the kind's own, stored marked as fine samples.
//
// Only a kind whose records a fine sample can be is accepted: a kind whose file names its live answer
// (provider.Contract.LiveAnswer). A live pass on any other kind is refused at load, with its reason.

// LiveKinds lists the kinds accepting a live pass, for the documentation's test.
func LiveKinds() []string {
	var kinds []string
	for _, kind := range provider.Kinds() {
		if kind.LiveAnswer != "" {
			kinds = append(kinds, kind.Kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}

// liveBinding is a checked live pass.
type liveBinding struct {
	function string
	// settingKey is where the cadence is stored (<plugin>_<key>); fallback its default; minimum its
	// lower bound.
	settingKey        string
	fallback, minimum int64
	// floor is the highest freshness floor of the live endpoints, in whole seconds.
	floor int64
	only  map[string]bool
	// kind is the kind of the provider declaring it.
	kind string
}

// ListedLive is a provider's live pass, in the plugin listing.
type ListedLive struct {
	Function       string   `json:"function"`
	CadenceSetting string   `json:"cadence_setting"`
	Endpoints      []string `json:"endpoints"`
}

// checkLive checks a provider's live pass and returns its binding; nil with no live pass.
func checkLive(plugin *Plugin, declaration contract.ProviderDeclaration, exports map[string]map[string]string) (
	*liveBinding, *contract.Refusal) {
	live := declaration.Live
	if live == nil {
		return nil, nil
	}
	if kindContract, _ := provider.ContractOf(declaration.Kind); kindContract.LiveAnswer == "" {
		return nil, contract.Refuse(plugin.Name, contract.RefusalLivePassInvalid, "the %s kind has no live pass: only %v accept one",
			declaration.Kind, LiveKinds())
	}
	if declaration.Probe != nil || declaration.Read != nil || plugin.script == nil {
		return nil, contract.Refuse(plugin.Name, contract.RefusalLivePassInvalid, "the %s provider's live pass needs a script, and "+
			"the provider is declarative", declaration.Kind)
	}
	if !contract.NamePattern.MatchString(live.Function) || exports[declaration.Kind][live.Function] != "function" {
		return nil, contract.Refuse(plugin.Name, contract.RefusalLivePassInvalid, "the script does not export the live function %s.%s",
			declaration.Kind, live.Function)
	}
	var setting *contract.SettingDeclaration
	for index := range plugin.Manifest.Settings {
		if plugin.Manifest.Settings[index].Key == live.CadenceSetting {
			setting = &plugin.Manifest.Settings[index]
		}
	}
	if setting == nil || setting.Type != "integer" || setting.Min == nil || *setting.Min < 1 || setting.Default == nil {
		return nil, contract.Refuse(plugin.Name, contract.RefusalLivePassInvalid, "the live cadence setting %q is not a declared "+
			"integer setting with a default and a min of at least 1 second", live.CadenceSetting)
	}
	fallback, ok := scripthost.IntegerOf(setting.Default)
	if !ok || float64(fallback) < *setting.Min {
		return nil, contract.Refuse(plugin.Name, contract.RefusalLivePassInvalid, "the live cadence setting %q has a default below "+
			"its min", live.CadenceSetting)
	}
	if len(live.Endpoints) == 0 {
		return nil, contract.Refuse(plugin.Name, contract.RefusalLivePassInvalid, "the live pass names no endpoint")
	}
	only := map[string]bool{}
	floor := int64(0)
	for _, name := range live.Endpoints {
		if _, declared := plugin.Endpoints[name]; !declared {
			return nil, contract.Refuse(plugin.Name, contract.RefusalLivePassInvalid, "the live pass names the endpoint %q, which the "+
				"manifest does not declare", name)
		}
		for _, endpoint := range plugin.Manifest.Endpoints {
			if endpoint.Name == name && int64(math.Ceil(endpoint.FloorSeconds)) > floor {
				floor = int64(math.Ceil(endpoint.FloorSeconds))
			}
			if endpoint.Name == name && endpoint.Stream {
				return nil, contract.Refuse(plugin.Name, contract.RefusalLivePassInvalid, "the live pass names the stream %q; a live "+
					"pass reads answers", name)
			}
		}
		only[name] = true
	}
	return &liveBinding{function: live.Function, settingKey: StorageKey(plugin.Name, live.CadenceSetting),
		fallback: fallback, minimum: int64(math.Ceil(*setting.Min)), floor: floor, only: only, kind: declaration.Kind}, nil
}

// listedLive renders a live pass for the listing; nil with none.
func listedLive(declaration contract.ProviderDeclaration) *ListedLive {
	if declaration.Live == nil {
		return nil
	}
	endpoints := append([]string{}, declaration.Live.Endpoints...)
	sort.Strings(endpoints)
	return &ListedLive{Function: declaration.Live.Function, CadenceSetting: declaration.Live.CadenceSetting,
		Endpoints: endpoints}
}

// liveInvoker calls a live function: the script's, under the live pass's max age and endpoints.
type liveInvoker struct {
	script    *scripthost.Script
	namespace string
	maxAge    time.Duration
	only      map[string]bool
}

func (i liveInvoker) invoke(ctx context.Context, session provider.Session, function string,
	arguments ...any) (any, []error, *scripthost.Failure) {
	return i.script.CallLive(ctx, session, i.namespace, function, i.maxAge, i.only, arguments...)
}

// LivePass is one loaded provider's live pass: what the collector runs and the rolling strip is
// bucketed by.
type LivePass struct {
	Plugin, Kind, ProviderKey string
	// SettingKey is where the cadence is stored, Fallback its value with no row and Minimum its
	// lower bound; FloorSeconds is the highest freshness floor of its endpoints, below which the
	// broker never serves it.
	SettingKey        string
	Fallback, Minimum int64
	FloorSeconds      int64
}

// Cadence is the live cadence in force: the stored setting -- its default with no row or an
// unreadable one, never below its lower bound -- raised to the endpoints' freshness floor.
func (p LivePass) Cadence(ctx context.Context, reader interface {
	Setting(ctx context.Context, key string) (string, bool, error)
}) time.Duration {
	return time.Duration(EffectiveLiveCadence(ctx, reader, p.SettingKey, p.Fallback, p.Minimum, p.FloorSeconds)) *
		time.Second
}

// EffectiveLiveCadence reads a live cadence setting, in whole seconds: the default with no row or
// one that is not a whole number, never below the minimum, raised to the floor.
func EffectiveLiveCadence(ctx context.Context, reader interface {
	Setting(ctx context.Context, key string) (string, bool, error)
}, key string, fallback, minimum, floor int64) int64 {
	seconds := fallback
	if reader != nil {
		if text, present, err := reader.Setting(ctx, key); err == nil && present {
			if parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
				seconds = parsed
			}
		}
	}
	if seconds < minimum {
		seconds = minimum
	}
	if seconds < floor {
		seconds = floor
	}
	return seconds
}

// LivePasses lists every loaded provider's live pass, built-ins first.
func (l *Loaded) LivePasses() []LivePass {
	var passes []LivePass
	for _, loaded := range l.Members {
		for _, declaration := range loaded.Manifest.Providers {
			key := declaration.Key
			if key == "" {
				key = loaded.Name
			}
			binding := loaded.lives[key]
			if binding == nil {
				continue
			}
			passes = append(passes, LivePass{Plugin: loaded.Name, Kind: declaration.Kind, ProviderKey: key,
				SettingKey: binding.settingKey, Fallback: binding.fallback, Minimum: binding.minimum,
				FloorSeconds: binding.floor})
		}
	}
	return passes
}

// liveOf is the live pass of the plugin's provider of a kind whose function it is, nil otherwise:
// what a plugin's own test calls when it calls that function (harnesscalls.go).
func (p *Plugin) liveOf(kind, function string) *liveBinding {
	for _, binding := range p.lives {
		if binding.kind == kind && binding.function == function {
			return binding
		}
	}
	return nil
}
