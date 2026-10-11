package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// interface_volume_ranking: interfaces by logged volume, each bar carrying the bytes, the
// client count behind it, the blocked connections and the east-west and north-south parts
// (docs/widget-catalogue.md, "Interface volume ranking"). A flow counts on the interface of
// its inside end; an interface's own figures add the second legs logged on it, as
// ReadVolumeWindow.ExitLegs gives them.

var interfaceVolumeRanking = Preset{
	Type: "interface_volume_ranking", Shape: "ranked_bars", Windowed: true,
	Providers: []string{"firewall_log"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "measure", Kind: KindEnum, Values: []string{"bytes", "connections", "clients"}, Default: "bytes",
			Data: true},
		{Key: "scope", Kind: KindEnum, Values: []string{scopeAll, scopeEastWest, scopeNorthSouth}, Default: scopeAll,
			Data: true},
		limitParam(10),
		{Key: "include_unlabelled", Kind: KindBoolean, Default: true, Data: true},
	},
	Serve: serveInterfaceVolumeRanking,
}

// InterfaceBar is one interface's bar.
type InterfaceBar struct {
	Interface InterfaceInfo `json:"interface"`
	Value     float64       `json:"value"`
	// Volume is the interface's own figures: its rows and its second legs.
	Volume Volume `json:"volume"`
	// The parts within the bar.
	EastWest     Volume `json:"east_west"`
	NorthSouth   Volume `json:"north_south"`
	ThisFirewall Volume `json:"this_firewall"`
	ExitLegs     Volume `json:"exit_legs"`
	// ClientsSeen are the distinct clients of the window's flows on the interface, out of
	// ClientsKnown, the client rows of the interface seen since the window began.
	ClientsSeen  int64 `json:"clients_seen"`
	ClientsKnown int64 `json:"clients_known"`
	// RulesNotLogging counts the rules naming the interface whose logging is off: their
	// matches produce no record at all.
	RulesNotLogging int64 `json:"rules_not_logging"`
}

// InterfaceRankingData is the ranking.
type InterfaceRankingData struct {
	Measure string         `json:"measure"`
	Bars    []InterfaceBar `json:"bars"`
	// Remainder is the interfaces past `limit`, and their figures.
	Remainder Remainder `json:"remainder"`
	// ExcludedUnlabelled is the interfaces left out by include_unlabelled = false.
	ExcludedUnlabelled *Remainder `json:"excluded_unlabelled,omitempty"`
	// Unattributed is the volume with no inside end: neither end is an interface's.
	Unattributed Volume `json:"unattributed"`
	// Total is every bar, the remainder, the excluded and the unattributed together.
	Total Volume `json:"total"`
	// RuleLogging counts the rules that do not log and name no single interface.
	FloatingRulesNotLogging   int64 `json:"floating_rules_not_logging"`
	UnresolvedRulesNotLogging int64 `json:"unresolved_rules_not_logging"`
	RulesLoggingNotReported   int64 `json:"rules_logging_not_reported"`
}

func serveInterfaceVolumeRanking(r *Request) (any, bool, error) {
	window, err := r.volumeWindow()
	if err != nil {
		return nil, false, err
	}
	cache, err := r.install()
	if err != nil {
		return nil, false, err
	}
	scope := r.params.text("scope")
	type parts struct{ own, eastWest, northSouth, thisFirewall, exitLegs store.VolumeFigures }
	figures := map[int64]*parts{}
	member := func(id int64) *parts {
		if figures[id] == nil {
			figures[id] = &parts{}
		}
		return figures[id]
	}
	var unattributed store.VolumeFigures
	hasRows := false
	for _, row := range window.Rows {
		if !scopeMatches(scope, row.VolumeKey) {
			continue
		}
		hasRows = true
		if row.ExitLegDevice != nil {
			info, known := cache.byDevice[*row.ExitLegDevice]
			if !known {
				addFigures(&unattributed, row.VolumeFigures)
				continue
			}
			bar := member(info.ID)
			addFigures(&bar.exitLegs, row.VolumeFigures)
			addFigures(&bar.own, row.VolumeFigures)
			continue
		}
		local := localInterface(row.VolumeKey)
		if local == nil {
			addFigures(&unattributed, row.VolumeFigures)
			continue
		}
		bar := member(*local)
		addFigures(&bar.own, row.VolumeFigures)
		switch row.TrafficScope {
		case scopeEastWest:
			addFigures(&bar.eastWest, row.VolumeFigures)
		case scopeNorthSouth:
			addFigures(&bar.northSouth, row.VolumeFigures)
		default:
			addFigures(&bar.thisFirewall, row.VolumeFigures)
		}
	}
	clients, err := r.db.ReadInterfaceClients(r.ctx, r.window.From, r.window.To)
	if err != nil {
		return nil, false, err
	}
	clientCounts := map[int64]store.InterfaceClients{}
	for _, count := range clients {
		clientCounts[count.InterfaceID] = count
	}
	logging, err := r.db.ReadRuleLogging(r.ctx)
	if err != nil {
		return nil, false, err
	}

	measure := r.params.text("measure")
	covered := r.response.Window.Covered != nil
	var (
		bars          []InterfaceBar
		excluded      Remainder
		excludedSum   store.VolumeFigures
		total         store.VolumeFigures
		membersRanked []ranked
	)
	byKey := map[string]InterfaceBar{}
	for _, info := range cache.interfaces {
		sums := figures[info.ID]
		if sums == nil {
			if !covered {
				// A silent member is drawn with an empty bar only where a measurement says
				// zero: an uncovered window measured nothing.
				continue
			}
			sums = &parts{}
		}
		addFigures(&total, sums.own)
		if !r.params.flag("include_unlabelled") && !info.Labelled {
			excluded.Members++
			addFigures(&excludedSum, sums.own)
			continue
		}
		bar := InterfaceBar{Interface: info, Volume: logged(sums.own), EastWest: logged(sums.eastWest),
			NorthSouth: logged(sums.northSouth), ThisFirewall: logged(sums.thisFirewall),
			ExitLegs: logged(sums.exitLegs), ClientsSeen: clientCounts[info.ID].Seen,
			ClientsKnown: clientCounts[info.ID].Known, RulesNotLogging: logging.NotLoggingByInterface[info.ID]}
		switch measure {
		case "clients":
			bar.Value = float64(bar.ClientsSeen)
		default:
			bar.Value = figuresValue(measure, sums.own)
		}
		byKey[interfaceKey(info.ID)] = bar
		membersRanked = append(membersRanked, ranked{key: interfaceKey(info.ID), value: bar.Value})
	}
	// Figures of an interface id the interface table no longer holds stay in the total.
	for id, sums := range figures {
		if _, known := cache.byID[id]; !known {
			addFigures(&unattributed, sums.own)
		}
	}
	addFigures(&total, unattributed)
	rankDescending(membersRanked)
	limit := int(r.params.integer("limit"))
	var remainderSum store.VolumeFigures
	remainder := Remainder{}
	for index, member := range membersRanked {
		bar := byKey[member.key]
		if index < limit {
			bars = append(bars, bar)
			continue
		}
		remainder.Members++
		addFigures(&remainderSum, toFigures(bar.Volume))
	}
	remainderVolume := logged(remainderSum)
	remainder.Volume = &remainderVolume
	data := InterfaceRankingData{Measure: measure, Bars: bars, Remainder: remainder,
		Unattributed: logged(unattributed), Total: logged(total),
		FloatingRulesNotLogging: logging.FloatingNotLogging, UnresolvedRulesNotLogging: logging.UnresolvedNotLogging,
		RulesLoggingNotReported: logging.NotReported}
	if data.Bars == nil {
		data.Bars = []InterfaceBar{}
	}
	if !r.params.flag("include_unlabelled") {
		volume := logged(excludedSum)
		excluded.Volume = &volume
		data.ExcludedUnlabelled = &excluded
	}
	return data, hasRows, nil
}

// toFigures turns logged figures back into store figures, to sum them.
func toFigures(volume Volume) store.VolumeFigures {
	return store.VolumeFigures{Bytes: volume.Bytes, AllowedBytes: volume.AllowedBytes,
		BlockedBytes: volume.BlockedBytes, UnknownBytes: volume.UnknownBytes,
		AllowedConnections: volume.AllowedConnections, BlockedConnections: volume.BlockedConnections,
		UnknownConnections: volume.UnknownConnections}
}
