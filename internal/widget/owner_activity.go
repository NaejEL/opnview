package widget

import (
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/store"
)

// owner_activity: one card per person -- their volume, their allowed and blocked
// connections, their machines as chips each carrying its own state, and an inline
// timeline -- and the residual Unassigned card (docs/widget-catalogue.md, "Per-person
// activity"). Ownership is never inferred: a machine is under a person because somebody
// said so. A person's figures are the sum of their machines' figures, read from the client
// family; the cards and the Unassigned card together make the owner family's total.

var ownerActivity = Preset{
	Type: "owner_activity", Shape: "cards", Windowed: true,
	Providers: []string{"firewall_log"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "owners", Kind: KindReferenceList, Reference: "owner", Data: true},
		{Key: "include_unassigned", Kind: KindBoolean, Default: true, Data: true},
		{Key: "measure", Kind: KindEnum, Values: []string{"bytes", "allowed", "blocked"}, Default: "bytes",
			Data: true},
		{Key: "scope", Kind: KindEnum, Values: []string{scopeAll, scopeEastWest, scopeNorthSouth}, Default: scopeAll,
			Data: true},
		{Key: "sort", Kind: KindEnum, Values: []string{"volume", "blocked", "name", "machines"}, Default: "volume",
			Data: true},
		{Key: "clients_per_card", Kind: KindInteger, Min: 1, Default: float64(6), Data: true},
		{Key: "show_timeline", Kind: KindBoolean, Default: true, Data: true},
	},
	Serve: serveOwnerActivity,
}

// Chip is one machine on a card, carrying its own state.
type Chip struct {
	Client ClientInfo `json:"client"`
	// State is measured, or no_data_in_period for a machine that reported nothing in the
	// window -- which is not a zero the reader might take for its absence.
	State  string  `json:"state"`
	Volume *Volume `json:"volume"`
	// CurrentRate is the machine's latest sampled rate, or its named absence.
	CurrentRate ChipRate `json:"current_rate"`
}

// ChipRate is a current rate, of the sampled measure.
type ChipRate struct {
	State            string   `json:"state"`
	Measure          string   `json:"measure"`
	InBitsPerSecond  *float64 `json:"in_bits_per_second"`
	OutBitsPerSecond *float64 `json:"out_bits_per_second"`
	OutMeasured      bool     `json:"out_measured"`
	SampledAt        *int64   `json:"sampled_at"`
}

// Card is one person, or the Unassigned group.
type Card struct {
	Key   string     `json:"key"`
	Owner *OwnerInfo `json:"owner"`
	Value float64    `json:"value"`
	// Volume is the card's figures over the selected scope.
	Volume Volume `json:"volume"`
	// Machines is the distinct machines with traffic in the window, counted, never summed.
	Machines      int64     `json:"machines"`
	Chips         []Chip    `json:"chips"`
	ChipRemainder Remainder `json:"chip_remainder"`
	Timeline      []Bucket  `json:"timeline,omitempty"`
}

// OwnerActivityData is the cards.
type OwnerActivityData struct {
	Measure    string `json:"measure"`
	Cards      []Card `json:"cards"`
	Unassigned *Card  `json:"unassigned"`
	// TotalsExcludeUnassigned says the Unassigned card is hidden and the totals below
	// exclude it.
	TotalsExcludeUnassigned bool   `json:"totals_exclude_unassigned"`
	Total                   Volume `json:"total"`
}

func serveOwnerActivity(r *Request) (any, bool, error) {
	owners, err := r.owners()
	if err != nil {
		return nil, false, err
	}
	if len(owners) == 0 {
		r.override = ConditionNoOwnerExists
	}
	volumes, err := r.clientVolumes()
	if err != nil {
		return nil, false, err
	}
	scope := r.params.text("scope")
	measure := r.params.text("measure")
	selected := r.idSet("owners")
	perClient := map[int64]store.VolumeFigures{}
	for id, volume := range volumes {
		var figures store.VolumeFigures
		for direction, sum := range volume.byDirection {
			if directionInScope(scope, direction) {
				addFigures(&figures, sum)
			}
		}
		perClient[id] = figures
	}
	limit := int(r.params.integer("clients_per_card"))
	rates, err := r.db.ReadCurrentRates(r.ctx, r.now)
	if err != nil {
		return nil, false, err
	}
	chipOf := func(info *ClientInfo) Chip {
		rate, sampled := rates.ByClient[info.ID]
		chip := Chip{Client: *info, State: "no_data_in_period", CurrentRate: sampledRate(rate, sampled)}
		if figures, present := perClient[info.ID]; present && (figures.Bytes > 0 || figures.Connections() > 0) {
			volume := logged(figures)
			chip.State, chip.Volume = "measured", &volume
		}
		return chip
	}
	build := func(key string, owner *OwnerInfo, members []*ClientInfo) Card {
		card := Card{Key: key, Owner: owner, Chips: []Chip{}}
		var figures store.VolumeFigures
		var chips []Chip
		for _, info := range members {
			chip := chipOf(info)
			if chip.Volume != nil {
				card.Machines++
				addFigures(&figures, toFigures(*chip.Volume))
			}
			chips = append(chips, chip)
		}
		sort.SliceStable(chips, func(i, j int) bool {
			a, b := chips[i].Volume, chips[j].Volume
			switch {
			case a == nil || b == nil:
				return a != nil && b == nil
			case a.Bytes != b.Bytes:
				return a.Bytes > b.Bytes
			}
			return chips[i].Client.ID < chips[j].Client.ID
		})
		var rest store.VolumeFigures
		for index, chip := range chips {
			if index < limit {
				card.Chips = append(card.Chips, chip)
				continue
			}
			card.ChipRemainder.Members++
			if chip.Volume != nil {
				addFigures(&rest, toFigures(*chip.Volume))
			}
		}
		restVolume := logged(rest)
		card.ChipRemainder.Volume = &restVolume
		card.Volume = logged(figures)
		card.Value = figuresValue(map[string]string{"allowed": "allowed", "blocked": "blocked"}[measure], figures)
		return card
	}

	data := OwnerActivityData{Measure: measure, Cards: []Card{}}
	var total store.VolumeFigures
	hasRows := len(volumes) > 0
	for _, owner := range owners {
		if selected != nil && !selected[owner.ID] {
			continue
		}
		var members []*ClientInfo
		err := r.db.WidgetRows(r.ctx, "widget_clients_of_owner", store.Period{},
			map[string]any{"owner_id": owner.ID}, func(row store.RowScanner) error {
				info, err := scanClient(row)
				if err == nil {
					members = append(members, info)
				}
				return err
			})
		if err != nil {
			return nil, false, err
		}
		copied := owner
		card := build("owner:"+strconv.FormatInt(owner.ID, 10), &copied, members)
		addFigures(&total, toFigures(card.Volume))
		data.Cards = append(data.Cards, card)
	}
	if r.params.flag("include_unassigned") && selected == nil {
		var members []*ClientInfo
		for id := range volumes {
			info, err := r.client(id)
			if err != nil {
				return nil, false, err
			}
			if info != nil && info.OwnerID == nil {
				members = append(members, info)
			}
		}
		sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
		card := build("unassigned", nil, members)
		addFigures(&total, toFigures(card.Volume))
		data.Unassigned = &card
	} else if !r.params.flag("include_unassigned") {
		data.TotalsExcludeUnassigned = true
	}
	sortCards(data.Cards, r.params.text("sort"))
	if r.params.flag("show_timeline") {
		if err := r.ownerTimelines(&data, scope, measure); err != nil {
			return nil, false, err
		}
	}
	data.Total = logged(total)
	return data, hasRows, nil
}

// sortCards orders the person cards.
func sortCards(cards []Card, order string) {
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		switch order {
		case "blocked":
			if a.Volume.BlockedConnections != b.Volume.BlockedConnections {
				return a.Volume.BlockedConnections > b.Volume.BlockedConnections
			}
		case "name":
			return a.Owner.DisplayName < b.Owner.DisplayName
		case "machines":
			if a.Machines != b.Machines {
				return a.Machines > b.Machines
			}
		default:
			if a.Value != b.Value {
				return a.Value > b.Value
			}
		}
		return a.Key < b.Key
	})
}

// ownerTimelines fills each card's inline timeline from the flows, per owner.
func (r *Request) ownerTimelines(data *OwnerActivityData, scope, measure string) error {
	width := r.bucketWidth()
	sums := map[string]map[int64]store.VolumeFigures{}
	parameters := r.windowParameters()
	parameters["width"] = width
	err := r.db.WidgetRows(r.ctx, "widget_owner_buckets", store.Period{}, parameters,
		func(row store.RowScanner) error {
			var (
				start     int64
				owner     store.NullInt64
				direction string
				figures   store.VolumeFigures
			)
			if err := row.Scan(&start, &owner, &direction, &figures.Bytes, &figures.AllowedBytes,
				&figures.BlockedBytes, &figures.UnknownBytes, &figures.AllowedConnections,
				&figures.BlockedConnections, &figures.UnknownConnections); err != nil {
				return err
			}
			if !directionInScope(scope, direction) {
				return nil
			}
			key := "unassigned"
			if owner.Valid {
				key = "owner:" + strconv.FormatInt(owner.Int64, 10)
			}
			if sums[key] == nil {
				sums[key] = map[int64]store.VolumeFigures{}
			}
			sum := sums[key][start]
			addFigures(&sum, figures)
			sums[key][start] = sum
			return nil
		})
	if err != nil {
		return err
	}
	figure := map[string]string{"allowed": "allowed", "blocked": "blocked"}[measure]
	timeline := func(key string) []Bucket {
		buckets := []Bucket{}
		for _, bucket := range bucketIntervals(r.window, width) {
			start := bucket.From - floorMod(bucket.From, width)
			sum, present := sums[key][start]
			entry := Bucket{From: bucket.From, To: bucket.To,
				Gap: isGapBucket(bucket, r.response.Window.Covered, r.response.Gaps)}
			if present || !entry.Gap {
				value := figuresValue(figure, sum)
				volume := logged(sum)
				entry.Value, entry.Volume = &value, &volume
			}
			buckets = append(buckets, entry)
		}
		return buckets
	}
	for index := range data.Cards {
		data.Cards[index].Timeline = timeline(data.Cards[index].Key)
	}
	if data.Unassigned != nil {
		data.Unassigned.Timeline = timeline("unassigned")
	}
	return nil
}
