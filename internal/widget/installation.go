package widget

import (
	"github.com/NaejEL/opnview/internal/store"
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
	err := r.Db.WidgetRows(r.Ctx, "widget_interfaces", store.Period{}, nil, func(row store.RowScanner) error {
		var (
			info              InterfaceInfo
			label             store.NullString
			vlan              store.NullInt64
			upstream, labeled int64
		)
		if err := row.Scan(&info.ID, &info.Identifier, &info.Device, &info.Description, &label,
			&info.LinkKind, &vlan, &upstream, &info.Label, &labeled); err != nil {
			return err
		}
		info.UserLabel = NullableText(label)
		info.Labelled = labeled == 1
		info.VLANTag = NullableNumber(vlan)
		info.Upstream = upstream == 1
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
	err = r.Db.WidgetRows(r.Ctx, "widget_owners", store.Period{}, nil, func(row store.RowScanner) error {
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
	err = r.Db.WidgetRows(r.Ctx, "widget_client", store.Period{}, map[string]any{"client_id": id},
		func(row store.RowScanner) error {
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
func ScanClient(row store.RowScanner) (*ClientInfo, error) {
	var (
		info                   ClientInfo
		interfaceID, owner     store.NullInt64
		assigned               store.NullInt64
		mac, hostname, address store.NullString
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

// Geolocation returns the geolocation of an address, no_row when none is stored.
func (r *Request) Geolocation(address string) (*GeoInfo, error) {
	cache, err := r.Install()
	if err != nil {
		return nil, err
	}
	if info, present := cache.geo[address]; present {
		return info, nil
	}
	info := &GeoInfo{LookupState: GeoNoRow}
	err = r.Db.WidgetRows(r.Ctx, "widget_geo", store.Period{}, map[string]any{"address": address},
		func(row store.RowScanner) error {
			var (
				ignored                 string
				country, name, operator store.NullString
				latitude, longitude     store.NullFloat64
				asn, build              store.NullInt64
			)
			if err := row.Scan(&ignored, &info.LookupState, &country, &name, &latitude, &longitude, &asn,
				&operator, &build); err != nil {
				return err
			}
			info.CountryCode = NullableText(country)
			info.CountryName = NullableText(name)
			info.Latitude = nullableFloat(latitude)
			info.Longitude = nullableFloat(longitude)
			info.ASN = NullableNumber(asn)
			info.Operator = NullableText(operator)
			info.DatasetBuildAt = NullableNumber(build)
			return nil
		})
	if err != nil {
		return nil, err
	}
	cache.geo[address] = info
	return info, nil
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
	err = r.Db.WidgetRows(r.Ctx, "widget_rule", store.Period{}, map[string]any{"rule_id": id},
		func(row store.RowScanner) error {
			var (
				scanned   RuleInfo
				automatic int64
			)
			if err := row.Scan(&scanned.ID, &scanned.PfLabel, &scanned.Description, &scanned.Action,
				&automatic); err != nil {
				return err
			}
			scanned.Automatic = automatic == 1
			info = &scanned
			return nil
		})
	if err != nil {
		return nil, err
	}
	cache.rules[id] = info
	return info, nil
}
