package request

import (
	"encoding/json"
	"sort"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// What a response names its members by: the interfaces, clients, owners, rules and
// geolocations of this installation, read once per request and only when a preset asks.
// Every label is the user's own where one is set and the firewall's discovered
// description otherwise; nothing is ever classified by what it is called.

// installation caches the rows one request reads.
type installation struct {
	Interfaces []InterfaceInfo
	ByID       map[int64]InterfaceInfo
	ByDevice   map[string]InterfaceInfo
	clients    map[int64]*ClientInfo
	owners     map[int64]string
	ownerList  []OwnerInfo
	geo        map[string]*GeoInfo
	rules      map[int64]*RuleInfo
}

// InterfaceInfo is how a response names an interface.
type InterfaceInfo struct {
	ID          int64   `json:"id"`
	Identifier  string  `json:"identifier"`
	Device      string  `json:"device"`
	Description string  `json:"description"`
	UserLabel   *string `json:"user_label"`
	// Label is the user's label where one is set, the discovered description otherwise.
	Label string `json:"label"`
	// Labelled says the user set a label.
	Labelled bool   `json:"labelled"`
	LinkKind string `json:"link_kind"`
	VLANTag  *int64 `json:"vlan_tag"`
	Upstream bool   `json:"upstream"`
}

// ClientInfo is how a response names a client.
type ClientInfo struct {
	ID           int64   `json:"id"`
	IdentityKind string  `json:"identity_kind"`
	IdentityKey  string  `json:"identity_key"`
	InterfaceID  *int64  `json:"interface_id"`
	MAC          *string `json:"mac"`
	Hostname     *string `json:"hostname"`
	LastAddress  *string `json:"last_address"`
	OwnerID      *int64  `json:"owner_id"`
	// OwnerAssignedAt is when somebody assigned the machine to its owner.
	OwnerAssignedAt *int64 `json:"owner_assigned_at"`
	// UnstableIdentity is the IEEE locally-administered bit of the MAC: a randomised
	// address does not identify a machine across sessions.
	UnstableIdentity bool  `json:"unstable_identity"`
	LastSeenAt       int64 `json:"last_seen_at"`
}

// OwnerInfo is a person, as somebody typed them in.
type OwnerInfo struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
}

// GeoInfo is the geolocation of one address, or the state of its absence.
type GeoInfo struct {
	// LookupState is resolved, miss or pending, or no_row for an address never looked up.
	LookupState    string   `json:"lookup_state"`
	CountryCode    *string  `json:"country_code"`
	CountryName    *string  `json:"country_name"`
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	ASN            *int64   `json:"asn"`
	Operator       *string  `json:"operator"`
	DatasetBuildAt *int64   `json:"dataset_build_at"`
}

// GeoNoRow is the state of an address no geolocation lookup has stored a row for.
const GeoNoRow = "no_row"

// RuleInfo is a rule as the firewall described it.
type RuleInfo struct {
	ID          int64  `json:"id"`
	PfLabel     string `json:"pf_label"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Automatic   bool   `json:"automatic"`
}

// Install returns the request's installation cache, reading the interfaces on first use.
func (r *Request) Install() (*installation, error) {
	if r.installation != nil {
		return r.installation, nil
	}
	cache := &installation{ByID: map[int64]InterfaceInfo{}, ByDevice: map[string]InterfaceInfo{},
		clients: map[int64]*ClientInfo{}, geo: map[string]*GeoInfo{}, rules: map[int64]*RuleInfo{}}
	err := r.Db.WidgetRows(r.Ctx, "widget_interfaces", vocab.Period{}, nil, func(row reads.RowScanner) error {
		var (
			info              InterfaceInfo
			label             reads.NullString
			vlan              reads.NullInt64
			labeled           int64
			upstream          bool
		)
		if err := row.Scan(&info.ID, &info.Identifier, &info.Device, &info.Description, &label,
			&info.LinkKind, &vlan, &upstream, &info.Label, &labeled); err != nil {
			return err
		}
		info.UserLabel = NullableText(label)
		info.Labelled = labeled == 1
		info.VLANTag = NullableNumber(vlan)
		info.Upstream = upstream
		cache.Interfaces = append(cache.Interfaces, info)
		cache.ByID[info.ID] = info
		cache.ByDevice[info.Device] = info
		return nil
	})
	if err != nil {
		return nil, err
	}
	r.installation = cache
	return cache, nil
}

// Owners returns every owner, reading them on first use.
func (r *Request) Owners() ([]OwnerInfo, error) {
	cache, err := r.Install()
	if err != nil {
		return nil, err
	}
	if cache.owners != nil {
		return cache.ownerList, nil
	}
	cache.owners = map[int64]string{}
	err = r.Db.WidgetRows(r.Ctx, "widget_owners", vocab.Period{}, nil, func(row reads.RowScanner) error {
		var owner OwnerInfo
		if err := row.Scan(&owner.ID, &owner.DisplayName); err != nil {
			return err
		}
		cache.owners[owner.ID] = owner.DisplayName
		cache.ownerList = append(cache.ownerList, owner)
		return nil
	})
	return cache.ownerList, err
}

// Client returns one client, or nil when no row holds the id.
func (r *Request) Client(id int64) (*ClientInfo, error) {
	cache, err := r.Install()
	if err != nil {
		return nil, err
	}
	if info, present := cache.clients[id]; present {
		return info, nil
	}
	var info *ClientInfo
	err = r.Db.WidgetRows(r.Ctx, "widget_client", vocab.Period{}, map[string]any{"client_id": id},
		func(row reads.RowScanner) error {
			scanned, err := ScanClient(row)
			info = scanned
			return err
		})
	if err != nil {
		return nil, err
	}
	cache.clients[id] = info
	return info, nil
}

// ScanClient reads one client row.
func ScanClient(row reads.RowScanner) (*ClientInfo, error) {
	var (
		info                   ClientInfo
		interfaceID, owner     reads.NullInt64
		assigned               reads.NullInt64
		mac, hostname, address reads.NullString
		unstable               int64
	)
	if err := row.Scan(&info.ID, &info.IdentityKind, &info.IdentityKey, &interfaceID, &mac, &hostname,
		&address, &owner, &unstable, &info.LastSeenAt, &assigned); err != nil {
		return nil, err
	}
	info.InterfaceID = NullableNumber(interfaceID)
	info.MAC = NullableText(mac)
	info.Hostname = NullableText(hostname)
	info.LastAddress = NullableText(address)
	info.OwnerID = NullableNumber(owner)
	info.OwnerAssignedAt = NullableNumber(assigned)
	info.UnstableIdentity = unstable == 1
	return &info, nil
}

// geolocationBatch bounds the addresses one widget_geo statement reads. A window placing more outside
// peers than this reads them in as many statements as it takes, each a bounded set of primary-key
// searches; at the 1 000 000-row seed a 30-day window places fewer.
const geolocationBatch = 20000

// PeerAddressColumn is the column a row names an outside peer by, in the core's reads and in a
// widget's own statements alike: a row a widget's statement returns with a text in it is placed
// (Place), so its geolocation is read with the others.
const PeerAddressColumn = "peer_address"

// Place records outside peers the request is placing: the geolocations of every address placed and
// not yet read are read together at the first one a script asks for (Geolocation), in one statement
// rather than one statement a peer. The core places the peers of every row it reads -- the rolling
// window (internal/widget/traffic/volume.go), the destinations (internal/widget/traffic/flows.go) and
// the peer_address column of a widget's own statement (host.query) -- so a widget that places peers
// is served by the batch without naming it.
func (r *Request) Place(addresses ...string) {
	for _, address := range addresses {
		if address == "" {
			continue
		}
		if r.placed == nil {
			r.placed = map[string]bool{}
		}
		r.placed[address] = true
	}
}

// Geolocation returns the geolocation of an address, no_row when none is stored. A miss reads the
// address together with every address placed and not yet read (Place).
func (r *Request) Geolocation(address string) (*GeoInfo, error) {
	cache, err := r.Install()
	if err != nil {
		return nil, err
	}
	if info, present := cache.geo[address]; present {
		return info, nil
	}
	pending := []string{address}
	for placed := range r.placed {
		if _, read := cache.geo[placed]; !read && placed != address {
			pending = append(pending, placed)
		}
	}
	r.placed = nil
	// In a stable order, so a batch boundary falls where it fell on the last run.
	sort.Strings(pending)
	for start := 0; start < len(pending); start += geolocationBatch {
		if err := r.readGeolocations(cache, pending[start:min(start+geolocationBatch, len(pending))]); err != nil {
			return nil, err
		}
	}
	return cache.geo[address], nil
}

// readGeolocations reads the geolocations of a bounded set of addresses into the cache: an address no
// row holds is cached as no_row, as a read of it alone answered.
func (r *Request) readGeolocations(cache *installation, addresses []string) error {
	encoded, err := json.Marshal(addresses)
	if err != nil {
		return err
	}
	err = r.Db.WidgetRows(r.Ctx, "widget_geo", vocab.Period{}, map[string]any{"addresses": string(encoded)},
		func(row reads.RowScanner) error {
			var (
				info                    GeoInfo
				address                 string
				country, name, operator reads.NullString
				latitude, longitude     reads.NullFloat64
				asn, build              reads.NullInt64
			)
			if err := row.Scan(&address, &info.LookupState, &country, &name, &latitude, &longitude, &asn,
				&operator, &build); err != nil {
				return err
			}
			info.CountryCode = NullableText(country)
			info.CountryName = NullableText(name)
			info.Latitude = NullableFloat(latitude)
			info.Longitude = NullableFloat(longitude)
			info.ASN = NullableNumber(asn)
			info.Operator = NullableText(operator)
			info.DatasetBuildAt = NullableNumber(build)
			cache.geo[address] = &info
			return nil
		})
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if _, read := cache.geo[address]; !read {
			cache.geo[address] = &GeoInfo{LookupState: GeoNoRow}
		}
	}
	return nil
}

// Rule returns one rule, or nil when no row holds the id.
func (r *Request) Rule(id int64) (*RuleInfo, error) {
	cache, err := r.Install()
	if err != nil {
		return nil, err
	}
	if info, present := cache.rules[id]; present {
		return info, nil
	}
	var info *RuleInfo
	err = r.Db.WidgetRows(r.Ctx, "widget_rule", vocab.Period{}, map[string]any{"rule_id": id},
		func(row reads.RowScanner) error {
			var (
				scanned   RuleInfo
				automatic bool
			)
			if err := row.Scan(&scanned.ID, &scanned.PfLabel, &scanned.Description, &scanned.Action,
				&automatic); err != nil {
				return err
			}
			scanned.Automatic = automatic
			info = &scanned
			return nil
		})
	if err != nil {
		return nil, err
	}
	cache.rules[id] = info
	return info, nil
}
