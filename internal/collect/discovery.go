package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Runtime discovery. Survey, "Runtime discovery" (i) and (ii): everything here is
// read at startup and refreshed periodically, and none of it is ever hardcoded,
// defaulted or inferred from a name.

// RefreshDiscovery reads the interfaces, the device-to-description map and the
// rules, writes them, and replaces the snapshot the collectors join against.
//
// A partial failure is not fatal and is not silent: whatever answered is stored,
// and the error says which endpoint did not. A snapshot that is missing an
// interface makes a flow report `not_found` on its join key, which is a state the
// screens render, not a row that disappears.
func (c *Collector) RefreshDiscovery(ctx context.Context) error {
	snapshot := newDiscovery()
	snapshot.RefreshedAt = c.now()
	var failures []error

	interfacesRead := true
	if err := c.discoverInterfaces(ctx, &snapshot); err != nil {
		failures = append(failures, err)
		interfacesRead = false
	}
	if err := c.discoverInterfaceNames(ctx, &snapshot); err != nil {
		failures = append(failures, err)
	}
	if err := c.discoverRules(ctx, &snapshot); err != nil {
		failures = append(failures, err)
	}
	if err := c.discoverClock(ctx); err != nil {
		failures = append(failures, err)
	}

	// The snapshot is published even when something failed, because the part that
	// answered is better than the part that is stale.
	c.setDiscovery(snapshot)

	// A change of which interfaces are upstream, or of the networks that count,
	// changes what is outside, so every stored address is placed again. The first
	// discovery of a run is such a change, which is also what clears any
	// remote-address client an earlier classification left behind. The derivation
	// itself compares what classification reads of the interfaces with what it
	// read last (derive.go), so an operator's edit of a network is caught there
	// too, at the next derivation, whichever pass runs it.
	if interfacesRead {
		changed, err := c.onLinkChanged(ctx)
		if err != nil {
			failures = append(failures, err)
		} else if changed {
			if err := c.derive(ctx, derivation{}); err != nil {
				failures = append(failures, err)
			}
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("collect: runtime discovery was incomplete: %w", joinErrors(failures))
	}
	return nil
}

// discoverInterfaces reads /api/interfaces/overview/interfaces_info.
func (c *Collector) discoverInterfaces(ctx context.Context, snapshot *Discovery) error {
	response, err := c.client.Call(ctx, opnsense.InterfacesInfo, opnsense.RequestOptions{})
	if err != nil {
		return err
	}
	if !response.OK() {
		return fmt.Errorf("collect: %s answered %s", opnsense.InterfacesInfo.Path, response.Outcome)
	}

	rows, err := decode.Rows(response.Body)
	if err != nil {
		return fmt.Errorf("collect: reading %s: %w", opnsense.InterfacesInfo.Path, err)
	}

	// The previous discovery's instant, read before this pass writes anything: an
	// address read then and now continues its holding, whatever order the interfaces are
	// listed and written in.
	var previousDiscovery *int64
	if latest, found, err := c.store.LatestInterfaceDiscoveryAt(ctx); err != nil {
		return err
	} else if found {
		previousDiscovery = &latest
	}

	now := c.now()
	for _, row := range rows {
		identifier, hasIdentifier := decode.String(row, "identifier")
		device, hasDevice := decode.String(row, "device")
		if !hasIdentifier || !hasDevice {
			// A row that names neither its configuration key nor its device cannot
			// be joined to anything, so storing it would create an interface no
			// flow could ever reach.
			continue
		}
		description, hasDescription := decode.String(row, "description")
		if !hasDescription {
			// The endpoint's own fallback, which the survey records: the
			// upper-cased identifier. It is not a name opnview invented.
			description = decode.UpperASCII(identifier)
		}
		linkType, _ := decode.String(row, "link_type")
		vlanTag := decode.IntPointer(row, "vlan_tag")
		gateways := gatewayAddresses(row)

		iface := store.Interface{
			Identifier:  identifier,
			Device:      device,
			Description: description,
			// status and enabled are stored verbatim, with no normalisation: the
			// survey establishes the two fields and NOT their encoding, and a
			// vocabulary written here would be invented rather than discovered.
			Status:   decode.StringPointer(row, "status"),
			Enabled:  decode.StringPointer(row, "enabled"),
			LinkType: linkType,
			LinkKind: linkKind(vlanTag),
			VLANTag:  vlanTag,
			// Upstream exactly when the response reports a gateway behind the
			// interface, and for no other reason.
			IsUpstream: len(gateways) > 0,
		}
		id, err := c.store.UpsertInterface(ctx, iface, now)
		if err != nil {
			return err
		}
		addresses, networks := interfaceAddresses(row, id, gateways)
		for _, address := range addresses {
			if err := c.store.UpsertInterfaceAddress(ctx, address, previousDiscovery, now); err != nil {
				return err
			}
		}
		// The networks the addresses cover are proposed as detected networks; the
		// store keeps what the operator decided about each.
		if err := c.store.DetectNetworks(ctx, id, networks, now); err != nil {
			return err
		}
		if iface.IsUpstream {
			snapshot.Upstream[id] = struct{}{}
		}
		snapshot.InterfaceIDByIdentifier[identifier] = id
		snapshot.InterfaceIDByDevice[device] = id
		snapshot.Identifiers = append(snapshot.Identifiers, opnsense.InterfaceName(identifier))
		snapshot.Devices[device] = struct{}{}
	}
	return nil
}

// gatewayAddresses reads `gateways[]`: a list of gateway ADDRESSES, at most one
// per address family, every gateway configured on the interface that is enabled
// and has an address (opnsense/core 26.7.3, Interfaces/Api/OverviewController.php
// and Routing/Gateways.php, getInterfaceGateway). An entry that is not an address
// is left out rather than guessed at.
func gatewayAddresses(row decode.Object) []netip.Addr {
	var gateways []netip.Addr
	entries, isList := row["gateways"].([]any)
	if !isList {
		return nil
	}
	for _, entry := range entries {
		text, isText := entry.(string)
		if !isText {
			continue
		}
		address, err := netip.ParseAddr(strings.TrimSpace(text))
		if err != nil {
			continue
		}
		gateways = append(gateways, address.Unmap())
	}
	return gateways
}

// interfaceAddresses reads `addr4` and `addr6`, each one string in the form
// "address/prefix length" or empty, and `ipv4[]` and `ipv6[]`, whose entries are
// objects whose `ipaddr` has that form (opnsense/core 26.7.3,
// Interfaces/Api/OverviewController.php, where each entry is built as
// `ipaddr` = address "/" subnetbits, with a `vhid` beside it for a CARP address).
// It adds the gateways, and returns the networks the addresses cover. An entry
// that does not parse is left out rather than guessed at.
func interfaceAddresses(row decode.Object, interfaceID int64, gateways []netip.Addr) (
	[]store.InterfaceAddress, []netip.Prefix) {
	var (
		addresses []store.InterfaceAddress
		networks  []netip.Prefix
	)
	add := func(field, text string) {
		prefix, ok := parseInterfacePrefix(text)
		if !ok {
			return
		}
		bits := int64(prefix.Bits())
		address := prefix.Addr()
		addresses = append(addresses, store.InterfaceAddress{
			InterfaceID:   interfaceID,
			SourceField:   field,
			Address:       address.String(),
			PrefixLength:  &bits,
			AddressFamily: familyOf(address),
		})
		networks = append(networks, prefix.Masked())
	}
	for _, field := range []string{store.SourceFieldAddr4, store.SourceFieldAddr6} {
		if text, present := decode.String(row, field); present {
			add(field, text)
		}
	}
	for _, field := range []string{store.SourceFieldIPv4, store.SourceFieldIPv6} {
		entries, isList := row[field].([]any)
		if !isList {
			continue
		}
		for _, entry := range entries {
			object, isObject := entry.(map[string]any)
			if !isObject {
				continue
			}
			if text, present := decode.String(object, "ipaddr"); present {
				add(field, text)
			}
		}
	}
	for _, gateway := range gateways {
		addresses = append(addresses, store.InterfaceAddress{
			InterfaceID:   interfaceID,
			SourceField:   store.SourceFieldGateways,
			Address:       gateway.String(),
			AddressFamily: familyOf(gateway),
		})
	}
	return addresses, networks
}

// parseInterfacePrefix reads "address/prefix length". A link-local address may
// carry its zone -- the device it is scoped to -- which a prefix cannot hold, so the
// zone is dropped: the address is the same on every interface whatever the zone.
func parseInterfacePrefix(text string) (netip.Prefix, bool) {
	slash := strings.LastIndexByte(text, '/')
	if slash < 0 {
		return netip.Prefix{}, false
	}
	address, err := netip.ParseAddr(strings.TrimSpace(text[:slash]))
	if err != nil {
		return netip.Prefix{}, false
	}
	bits, err := strconv.Atoi(strings.TrimSpace(text[slash+1:]))
	if err != nil {
		return netip.Prefix{}, false
	}
	prefix := netip.PrefixFrom(address.WithZone("").Unmap(), bits)
	return prefix, prefix.IsValid()
}

// familyOf is 4 or 6.
func familyOf(address netip.Addr) int64 {
	if address.Is4() {
		return 4
	}
	return 6
}

// linkKind is opnview's normalisation of the raw link type into the closed class
// the schema constrains.
//
// IT DELIBERATELY CLASSIFIES LESS THAN THE SCHEMA ALLOWS, and this is a finding
// rather than an oversight. The schema's vocabulary is physical, vlan, tunnel and
// other, and `interface.is_tunnel` derives from it. The only field whose MEANING
// the survey establishes here is `vlan_tag` — "a VLAN's tag" — so a VLAN is
// recognisable. It does not establish the value set of `link_type`, so telling a
// physical link from a tunnel would mean matching tokens nobody has recorded,
// which is the defect this project exists not to commit. Everything that is not a
// VLAN is therefore `other`, and no discovered interface reads as a tunnel until
// the real link_type vocabulary is recorded against a live firewall.
func linkKind(vlanTag *int64) string {
	if vlanTag != nil {
		return "vlan"
	}
	return "other"
}

// discoverInterfaceNames reads /api/diagnostics/interface/get_interface_names,
// the first of the two first-class join keys: the raw device name the filter log
// reports, to the user-given description.
func (c *Collector) discoverInterfaceNames(ctx context.Context, snapshot *Discovery) error {
	response, err := c.client.Call(ctx, opnsense.InterfaceNames, opnsense.RequestOptions{})
	if err != nil {
		return err
	}
	if !response.OK() {
		return fmt.Errorf("collect: %s answered %s", opnsense.InterfaceNames.Path, response.Outcome)
	}

	var names map[string]string
	if err := json.Unmarshal(response.Body, &names); err != nil {
		return fmt.Errorf("collect: reading %s: %w", opnsense.InterfaceNames.Path, err)
	}

	now := c.now()
	for device, description := range names {
		if device == "" {
			continue
		}
		var interfaceID *int64
		if id, present := snapshot.InterfaceIDByDevice[device]; present {
			interfaceID = &id
		}
		if err := c.store.UpsertInterfaceMapEntry(ctx, device, description, interfaceID, now); err != nil {
			return err
		}
		if _, present := snapshot.InterfaceIDByDevice[device]; !present {
			snapshot.Devices[device] = struct{}{}
		}
	}
	return nil
}

// discoverRules reads /api/firewall/filter/search_rule, the second join key.
//
// rowCount -1 retrieves everything, which is what the survey establishes for this
// endpoint. The %-prefixed twins are read in preference to the human-facing
// fields, because the latter are localised for legacy rows and machine logic must
// never read a translated value.
func (c *Collector) discoverRules(ctx context.Context, snapshot *Discovery) error {
	response, err := c.client.Call(ctx, opnsense.SearchRule, opnsense.RequestOptions{
		Form: opnsense.Pagination(1, -1),
	})
	if err != nil {
		return err
	}
	if !response.OK() {
		return fmt.Errorf("collect: %s answered %s", opnsense.SearchRule.Path, response.Outcome)
	}

	rows, err := decode.Rows(response.Body)
	if err != nil {
		return fmt.Errorf("collect: reading %s: %w", opnsense.SearchRule.Path, err)
	}

	now := c.now()
	for _, row := range rows {
		pfLabel, present := decode.String(row, "uuid")
		if !present {
			continue
		}
		description, hasDescription := decode.String(row, "description")
		if !hasDescription {
			// A rule with no description is normal — an automatic rule often has
			// none — and the column is NOT NULL, so the empty string is the honest
			// value. It is not a name and nothing reads it as one.
			description = ""
		}
		rule := store.Rule{
			PfLabel:     pfLabel,
			Description: description,
			Action:      normaliseRuleAction(rawTwin(row, "action")),
			Direction:   normaliseRuleDirection(rawTwin(row, "direction")),
			LogsMatches: decode.Flag(row, "log"),
			Enabled:     decode.Flag(row, "enabled"),
			Interface:   ruleInterface(row),
			Legacy:      decode.Flag(row, "legacy"),
			IsAutomatic: decode.FlagOrFalse(decode.Flag(row, "is_automatic")),
		}
		id, err := c.store.UpsertRule(ctx, rule, now)
		if err != nil {
			return err
		}
		snapshot.RuleIDByPfLabel[pfLabel] = id
	}
	return nil
}

// ruleInterface reads the rule's `interface` field verbatim. An empty value is a
// floating rule, which applies on every interface, and is kept as the empty
// string; a field the row does not carry is nil, which is not reported.
func ruleInterface(row decode.Object) *string {
	raw, present := row["interface"]
	if !present || raw == nil {
		return nil
	}
	text, isText := raw.(string)
	if !isText {
		return nil
	}
	trimmed := strings.TrimSpace(text)
	return &trimmed
}

// rawTwin reads the %-prefixed raw value in preference to the human-facing one.
// For legacy rows the human-facing fields are localised and the twins carry the
// raw values machine logic must use. Survey, "Runtime discovery" (ii).
func rawTwin(row decode.Object, field string) string {
	if value, present := decode.String(row, "%"+field); present {
		return value
	}
	value, _ := decode.String(row, field)
	return value
}

// normaliseRuleAction maps the endpoint's raw action onto the closed vocabulary
// the schema constrains. An action outside it is `unknown`, never silently folded
// into `pass` or `block`: a rule whose effect opnview cannot name must not be
// rendered as one that permits or denies.
func normaliseRuleAction(raw string) string {
	switch decode.LowerASCII(raw) {
	case "pass":
		return "pass"
	case "block":
		return "block"
	case "reject":
		return "reject"
	default:
		return "unknown"
	}
}

// normaliseRuleDirection maps the raw direction, or reports it as not given.
func normaliseRuleDirection(raw string) *string {
	switch decode.LowerASCII(raw) {
	case "in":
		direction := "in"
		return &direction
	case "out":
		direction := "out"
		return &direction
	case "any", "":
		if raw == "" {
			return nil
		}
		direction := "any"
		return &direction
	default:
		return nil
	}
}

// discoverClock measures the firewall's offset from UTC from its own clock, survey
// gap 7, and puts it in force for every timestamp read after it. A failure leaves
// the last measurement in force rather than falling back to zero: a zone does not
// change because one request failed.
func (c *Collector) discoverClock(ctx context.Context) error {
	readAt := c.clock.Now()
	body, ok, err := c.readObject(ctx, opnsense.SystemTime)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("collect: %s did not answer, so the firewall's offset from UTC "+
			"stays at %ds", opnsense.SystemTime.Path, c.FirewallOffsetSeconds())
	}
	datetime, present := decode.String(body, "datetime")
	if !present {
		return fmt.Errorf("collect: %s carries no datetime, so the firewall's offset from UTC "+
			"stays at %ds", opnsense.SystemTime.Path, c.FirewallOffsetSeconds())
	}
	offset, err := decode.FirewallOffsetSeconds(datetime, readAt)
	if err != nil {
		return err
	}
	c.mutex.Lock()
	c.firewallOffsetSeconds, c.offsetMeasured = offset, true
	c.mutex.Unlock()
	return nil
}
