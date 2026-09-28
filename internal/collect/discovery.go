package collect

import (
	"context"
	"encoding/json"
	"fmt"

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

	if err := c.discoverInterfaces(ctx, &snapshot); err != nil {
		failures = append(failures, err)
	}
	if err := c.discoverInterfaceNames(ctx, &snapshot); err != nil {
		failures = append(failures, err)
	}
	if err := c.discoverRules(ctx, &snapshot); err != nil {
		failures = append(failures, err)
	}

	// The snapshot is published even when something failed, because the part that
	// answered is better than the part that is stale.
	c.setDiscovery(snapshot)

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
		}
		id, err := c.store.UpsertInterface(ctx, iface, now)
		if err != nil {
			return err
		}
		snapshot.InterfaceIDByIdentifier[identifier] = id
		snapshot.InterfaceIDByDevice[device] = id
		snapshot.Identifiers = append(snapshot.Identifiers, opnsense.InterfaceName(identifier))
		snapshot.Devices[device] = struct{}{}
	}
	return nil
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
