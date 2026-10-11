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
	interfaces []InterfaceInfo
	byID       map[int64]InterfaceInfo
	byDevice   map[string]InterfaceInfo
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

// geoNoRow is the state of an address no geolocation lookup has stored a row for.
const geoNoRow = "no_row"

// RuleInfo is a rule as the firewall described it.
type RuleInfo struct {
	ID          int64  `json:"id"`
	PfLabel     string `json:"pf_label"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Automatic   bool   `json:"automatic"`
}

// install returns the request's installation cache, reading the interfaces on first use.
func (r *Request) install() (*installation, error) {
	if r.installation != nil {
		return r.installation, nil
	}
	cache := &installation{byID: map[int64]InterfaceInfo{}, byDevice: map[string]InterfaceInfo{},
		clients: map[int64]*ClientInfo{}, geo: map[string]*GeoInfo{}, rules: map[int64]*RuleInfo{}}
	err := r.db.WidgetRows(r.ctx, "widget_interfaces", store.Period{}, nil, func(row store.RowScanner) error {
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
		info.UserLabel = nullableText(label)
		info.Labelled = labeled == 1
		info.VLANTag = nullableNumber(vlan)
		info.Upstream = upstream == 1
		cache.interfaces = append(cache.interfaces, info)
		cache.byID[info.ID] = info
		cache.byDevice[info.Device] = info
		return nil
	})
	if err != nil {
		return nil, err
	}
	r.installation = cache
	return cache, nil
}

// owners returns every owner, reading them on first use.
func (r *Request) owners() ([]OwnerInfo, error) {
	cache, err := r.install()
	if err != nil {
		return nil, err
	}
	if cache.owners != nil {
		return cache.ownerList, nil
	}
	cache.owners = map[int64]string{}
	err = r.db.WidgetRows(r.ctx, "widget_owners", store.Period{}, nil, func(row store.RowScanner) error {
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

// client returns one client, or nil when no row holds the id.
func (r *Request) client(id int64) (*ClientInfo, error) {
	cache, err := r.install()
	if err != nil {
		return nil, err
	}
	if info, present := cache.clients[id]; present {
		return info, nil
	}
	var info *ClientInfo
	err = r.db.WidgetRows(r.ctx, "widget_client", store.Period{}, map[string]any{"client_id": id},
		func(row store.RowScanner) error {
			scanned, err := scanClient(row)
			info = scanned
			return err
		})
	if err != nil {
		return nil, err
	}
	cache.clients[id] = info
	return info, nil
}

// scanClient reads one client row.
func scanClient(row store.RowScanner) (*ClientInfo, error) {
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
	info.InterfaceID = nullableNumber(interfaceID)
	info.MAC = nullableText(mac)
	info.Hostname = nullableText(hostname)
	info.LastAddress = nullableText(address)
	info.OwnerID = nullableNumber(owner)
	info.OwnerAssignedAt = nullableNumber(assigned)
	info.UnstableIdentity = unstable == 1
	return &info, nil
}

// geolocation returns the geolocation of an address, no_row when none is stored.
func (r *Request) geolocation(address string) (*GeoInfo, error) {
	cache, err := r.install()
	if err != nil {
		return nil, err
	}
	if info, present := cache.geo[address]; present {
		return info, nil
	}
	info := &GeoInfo{LookupState: geoNoRow}
	err = r.db.WidgetRows(r.ctx, "widget_geo", store.Period{}, map[string]any{"address": address},
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
			info.CountryCode = nullableText(country)
			info.CountryName = nullableText(name)
			info.Latitude = nullableFloat(latitude)
			info.Longitude = nullableFloat(longitude)
			info.ASN = nullableNumber(asn)
			info.Operator = nullableText(operator)
			info.DatasetBuildAt = nullableNumber(build)
			return nil
		})
	if err != nil {
		return nil, err
	}
	cache.geo[address] = info
	return info, nil
}

// rule returns one rule, or nil when no row holds the id.
func (r *Request) rule(id int64) (*RuleInfo, error) {
	cache, err := r.install()
	if err != nil {
		return nil, err
	}
	if info, present := cache.rules[id]; present {
		return info, nil
	}
	var info *RuleInfo
	err = r.db.WidgetRows(r.ctx, "widget_rule", store.Period{}, map[string]any{"rule_id": id},
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
