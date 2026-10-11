package widget

import (
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// traffic_composition: logged volume as parts of the period's whole, grouped by client,
// interface, protocol or service port, the named slices then one Other slice carrying the
// rest (docs/widget-catalogue.md, "Traffic composition, now"). Application is not a
// grouping: nothing in the model classifies a flow as one (G12).

var trafficComposition = Preset{
	Type: "traffic_composition", Shape: "donut", Windowed: true,
	Providers: []string{"firewall_log", "measurement_sample"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "group_by", Kind: KindEnum, Values: []string{"client", "interface", "protocol", "service_port"},
			Default: "interface", Data: true},
		{Key: "measure", Kind: KindEnum, Values: []string{"bytes", "connections"}, Default: "bytes", Data: true},
		{Key: "slices", Kind: KindInteger, Min: 1, Default: float64(6), Data: true},
		interfacesParam(),
		clientsParam(),
		{Key: "scope", Kind: KindEnum, Values: []string{scopeAll, scopeEastWest, scopeNorthSouth}, Default: scopeAll,
			Data: true},
		{Key: "show_other", Kind: KindBoolean, Default: true},
	},
	Serve: serveTrafficComposition,
}

// Slice is one part of the whole.
type Slice struct {
	Key       string         `json:"key"`
	Interface *InterfaceInfo `json:"interface,omitempty"`
	Client    *ClientInfo    `json:"client,omitempty"`
	Protocol  *string        `json:"protocol,omitempty"`
	Port      *int64         `json:"port,omitempty"`
	Value     float64        `json:"value"`
	Volume    Volume         `json:"volume"`
}

// CompositionData is the donut.
type CompositionData struct {
	// GroupBy is the grouping dimension, always on screen.
	GroupBy string  `json:"group_by"`
	Measure string  `json:"measure"`
	Slices  []Slice `json:"slices"`
	// Other is every member past `slices`. It is always returned, so the slices and it
	// make the whole; whether it is drawn is show_other's, which is rendering-only (a
	// truncation is never silent, A8).
	Other Remainder `json:"other"`
	// Whole names what the total is the whole of, and Total is it.
	Whole string `json:"whole"`
	Total Volume `json:"total"`
	// CurrentRates are the sampled rates of now -- per direction, and per client or
	// interface for those groupings -- the second measure, never summed with the first.
	// traffic/top carries no protocol and no port, so the other two groupings have
	// none by member (the 5A table's item 4, cannot).
	CurrentRates *CurrentRates `json:"current_rates"`
}

// directionInScope reports whether a client-family direction is in a scope.
func directionInScope(scope, direction string) bool {
	switch scope {
	case scopeEastWest:
		return direction == directionInterInterface
	case scopeNorthSouth:
		return direction == directionOutbound || direction == directionInbound
	}
	return true
}

func serveTrafficComposition(r *Request) (any, bool, error) {
	cache, err := r.install()
	if err != nil {
		return nil, false, err
	}
	groupBy := r.params.text("group_by")
	measure := r.params.text("measure")
	scope := r.params.text("scope")
	interfaces := r.idSet("interfaces")
	clients := r.idSet("clients")
	parts := map[string]*Slice{}
	sums := map[string]*store.VolumeFigures{}
	add := func(key string, slice Slice, figures store.VolumeFigures) {
		if parts[key] == nil {
			copied := slice
			copied.Key = key
			parts[key] = &copied
			sums[key] = &store.VolumeFigures{}
		}
		addFigures(sums[key], figures)
	}
	data := CompositionData{GroupBy: groupBy, Measure: measure, Slices: []Slice{}}
	hasRows := false

	switch {
	case groupBy == "client":
		data.Whole = "traffic_with_an_inside_client"
		volumes, err := r.clientVolumes()
		if err != nil {
			return nil, false, err
		}
		for id, volume := range volumes {
			info, err := r.client(id)
			if err != nil {
				return nil, false, err
			}
			if info == nil || (clients != nil && !clients[id]) ||
				(interfaces != nil && (info.InterfaceID == nil || !interfaces[*info.InterfaceID])) {
				continue
			}
			var figures store.VolumeFigures
			for direction, sum := range volume.byDirection {
				if directionInScope(scope, direction) {
					addFigures(&figures, sum)
				}
			}
			if figures.Bytes == 0 && figures.Connections() == 0 {
				continue
			}
			hasRows = true
			add(clientKey(id), Slice{Client: info}, figures)
		}
	case groupBy == "interface" && clients == nil:
		data.Whole = "all_traffic"
		window, err := r.volumeWindow()
		if err != nil {
			return nil, false, err
		}
		for _, row := range window.Rows {
			if row.ExitLegDevice != nil || !scopeMatches(scope, row.VolumeKey) {
				continue
			}
			local := localInterface(row.VolumeKey)
			if interfaces != nil && (local == nil || !interfaces[*local]) {
				continue
			}
			hasRows = true
			if local == nil {
				add("no_interface", Slice{}, row.VolumeFigures)
				continue
			}
			slice := Slice{}
			if info, known := cache.byID[*local]; known {
				copied := info
				slice.Interface = &copied
			}
			add(interfaceKey(*local), slice, row.VolumeFigures)
		}
	default:
		data.Whole = "all_traffic"
		if err := r.coverFlows("composition_beyond_flow_horizon"); err != nil {
			return nil, false, err
		}
		parameters := r.windowParameters()
		parameters["scope"] = scope
		parameters["interface_ids"] = jsonIDs(r.ids("interfaces"))
		parameters["client_ids"] = jsonIDs(r.ids("clients"))
		name := map[string]string{"protocol": "widget_composition_protocol",
			"service_port": "widget_composition_port", "interface": "widget_composition_interface"}[groupBy]
		err := r.db.WidgetRows(r.ctx, name, store.Period{}, parameters, func(row store.RowScanner) error {
			var (
				protocol store.NullString
				port     store.NullInt64
				figures  store.VolumeFigures
				target   any = &protocol
			)
			if groupBy != "protocol" {
				target = &port
			}
			if err := row.Scan(target, &figures.Bytes, &figures.AllowedBytes, &figures.BlockedBytes,
				&figures.UnknownBytes, &figures.AllowedConnections, &figures.BlockedConnections,
				&figures.UnknownConnections); err != nil {
				return err
			}
			hasRows = true
			if groupBy == "interface" {
				if !port.Valid {
					add("no_interface", Slice{}, figures)
					return nil
				}
				slice := Slice{}
				if info, known := cache.byID[port.Int64]; known {
					copied := info
					slice.Interface = &copied
				}
				add(interfaceKey(port.Int64), slice, figures)
				return nil
			}
			if groupBy == "service_port" {
				if !port.Valid {
					add("port:not_recorded", Slice{}, figures)
					return nil
				}
				add("port:"+strconv.FormatInt(port.Int64, 10), Slice{Port: nullableNumber(port)}, figures)
				return nil
			}
			add("protocol:"+protocol.String, Slice{Protocol: nullableText(protocol)}, figures)
			return nil
		})
		if err != nil {
			return nil, false, err
		}
	}

	var members []ranked
	var total store.VolumeFigures
	for key := range parts {
		addFigures(&total, *sums[key])
		members = append(members, ranked{key: key, value: figuresValue(measure, *sums[key])})
	}
	rankDescending(members)
	var otherSum store.VolumeFigures
	other := Remainder{}
	for index, member := range members {
		if index < int(r.params.integer("slices")) {
			slice := *parts[member.key]
			slice.Value = member.value
			slice.Volume = logged(*sums[member.key])
			data.Slices = append(data.Slices, slice)
			continue
		}
		other.Members++
		addFigures(&otherSum, *sums[member.key])
	}
	data.Total = logged(total)
	volume := logged(otherSum)
	other.Volume = &volume
	data.Other = other
	rates, err := r.currentRates(groupBy)
	if err != nil {
		return nil, false, err
	}
	data.CurrentRates = rates
	return data, hasRows, nil
}
