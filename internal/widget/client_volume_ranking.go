package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// client_volume_ranking: clients by logged volume, with their host name, MAC, last
// address, interface, blocked connections and distinct destinations; a randomised MAC is
// badged unstable identity (docs/widget-catalogue.md, "Client volume ranking"). The
// ranking is per machine, which is not the per-person question.

var clientVolumeRanking = Preset{
	Type: "client_volume_ranking", Shape: "ranked_bars", Windowed: true,
	Providers: []string{"firewall_log", "dhcp_lease"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		interfacesParam(),
		{Key: "identity", Kind: KindEnum, Values: []string{"any", "dhcp_client_id", "mac", "address_in_interface"},
			Default: "any", Data: true},
		limitParam(10),
		{Key: "include_unstable", Kind: KindBoolean, Default: true, Data: true},
		{Key: "measure", Kind: KindEnum, Values: []string{"bytes", "connections", "destinations"}, Default: "bytes",
			Data: true},
	},
	Serve: serveClientVolumeRanking,
}

// ClientBar is one client's bar.
type ClientBar struct {
	Client    ClientInfo     `json:"client"`
	Interface *InterfaceInfo `json:"interface"`
	Value     float64        `json:"value"`
	Volume    Volume         `json:"volume"`
	// Destinations is the distinct far ends the client reached in the window, counted
	// over the hours and never summed.
	Destinations int64 `json:"destinations"`
}

// ClientRankingData is the ranking.
type ClientRankingData struct {
	Measure   string      `json:"measure"`
	Bars      []ClientBar `json:"bars"`
	Remainder Remainder   `json:"remainder"`
	// Excluded is the clients the identity and include_unstable keys left out.
	Excluded Remainder `json:"excluded"`
	Total    Volume    `json:"total"`
}

func serveClientVolumeRanking(r *Request) (any, bool, error) {
	volumes, err := r.clientVolumes()
	if err != nil {
		return nil, false, err
	}
	cache, err := r.install()
	if err != nil {
		return nil, false, err
	}
	selected := r.idSet("interfaces")
	identity := r.params.text("identity")
	measure := r.params.text("measure")
	var (
		members     []ranked
		bars        = map[string]ClientBar{}
		excluded    Remainder
		excludedSum store.VolumeFigures
		total       store.VolumeFigures
	)
	for id, volume := range volumes {
		info, err := r.client(id)
		if err != nil {
			return nil, false, err
		}
		if info == nil {
			continue
		}
		if selected != nil && (info.InterfaceID == nil || !selected[*info.InterfaceID]) {
			continue
		}
		addFigures(&total, volume.figures)
		if (identity != "any" && info.IdentityKind != identity) ||
			(!r.params.flag("include_unstable") && info.UnstableIdentity) {
			excluded.Members++
			addFigures(&excludedSum, volume.figures)
			continue
		}
		bar := ClientBar{Client: *info, Volume: logged(volume.figures), Destinations: int64(len(volume.peers))}
		if info.InterfaceID != nil {
			if iface, known := cache.byID[*info.InterfaceID]; known {
				copied := iface
				bar.Interface = &copied
			}
		}
		if measure == "destinations" {
			bar.Value = float64(bar.Destinations)
		} else {
			bar.Value = figuresValue(measure, volume.figures)
		}
		bars[clientKey(id)] = bar
		members = append(members, ranked{key: clientKey(id), value: bar.Value})
	}
	rankDescending(members)
	data := ClientRankingData{Measure: measure, Bars: []ClientBar{}, Total: logged(total)}
	var remainderSum store.VolumeFigures
	limit := int(r.params.integer("limit"))
	for index, member := range members {
		if index < limit {
			data.Bars = append(data.Bars, bars[member.key])
			continue
		}
		data.Remainder.Members++
		addFigures(&remainderSum, toFigures(bars[member.key].Volume))
	}
	remainderVolume := logged(remainderSum)
	data.Remainder.Volume = &remainderVolume
	excludedVolume := logged(excludedSum)
	excluded.Volume = &excludedVolume
	data.Excluded = excluded
	return data, len(volumes) > 0, nil
}
