package derive

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// pushIndexReader is the reader the broker records the interface-index read under.
const pushIndexReader = "push"

// IndexMap is a NetFlow exporter's interface indexes as the firewall names them: what a record's two
// indexes are mapped with at reception (NJ3 of the amendment "the NetFlow join with every interface
// captured").
type IndexMap struct {
	// ByIndex maps a kernel index to its device and, when an assignment names it, its identifier.
	ByIndex map[uint64]push.Interface
	// Loopback is the loopback interface's kernel index, which a record delivered to the firewall
	// itself carries as its egress index (survey S6b); 0 when no row established it.
	Loopback uint64
}

// Map is what an index names (NJ3): index 0 has no receiving interface, the loopback's index is local
// delivery, an index the map holds is that interface, and any other -- or none -- is unmapped.
func (m IndexMap) Map(index *int64) netflow.MappedIndex {
	switch {
	case index == nil || *index < 0:
		return netflow.MappedIndex{Role: netflow.RoleUnmapped}
	case *index == 0:
		return netflow.MappedIndex{Role: netflow.RoleNoReceivingInterface}
	case m.Loopback != 0 && uint64(*index) == m.Loopback:
		return netflow.MappedIndex{Role: netflow.RoleLocalDelivery}
	}
	mapped, found := m.ByIndex[uint64(*index)]
	if !found || mapped.Device == "" {
		return netflow.MappedIndex{Role: netflow.RoleUnmapped}
	}
	device := mapped.Device
	result := netflow.MappedIndex{Role: netflow.RoleInterface, Device: &device}
	if mapped.Identifier != "" {
		identifier := mapped.Identifier
		result.Identifier = &identifier
	}
	return result
}

// InterfaceIndexes maps a NetFlow exporter's interface indexes to the firewall's interfaces (PS11 of
// the push amendment), as the hub maps an index field of a plugin's own table: ReadIndexMap's map.
func (c *Deriver) InterfaceIndexes(ctx context.Context) (map[uint64]push.Interface, error) {
	read, err := c.ReadIndexMap(ctx, false)
	if err != nil {
		return nil, err
	}
	return read.ByIndex, nil
}

// ReadIndexMap reads the index map through the broker, at discovery's max age, or afresh when fresh is
// set: GET /api/interfaces/overview/interfaces_info/1, whose `details` argument adds to each row its
// `statistics`, the output of OPNsense's `ifinfo` per device, whose `index` is the kernel interface
// index ng_netflow exports (opnsense/core 26.7.3, Interfaces/Api/OverviewController.php parseIfInfo
// and src/etc/inc/interfaces.lib.inc legacy_interface_stats; survey, Push sources, S6). OPNsense's own
// NetFlow aggregation maps an index the same way (src/opnsense/scripts/netflow/lib/parse.py). A row
// carrying no index maps nothing: an index is never matched by position, name or count.
//
// The loopback's index is the index of the row whose `flags` -- the interface flags `ifconfig` prints,
// which legacy_interfaces_details lower-cases into a list and parseIfInfo copies into the row -- hold
// `loopback`, IFF_LOOPBACK's name (survey, Push sources, S6b). It is read from that flag and never
// from a device name; with no such row, or two, it is not established.
func (c *Deriver) ReadIndexMap(ctx context.Context, fresh bool) (IndexMap, error) {
	maxAge := c.DiscoveryMaxAge()
	if fresh {
		maxAge = time.Duration(0)
	}
	response, err := c.Read(ctx, pushIndexReader, opnsense.InterfacesInfo,
		opnsense.RequestOptions{Arguments: []string{"1"}}, maxAge)
	if err != nil {
		return IndexMap{}, err
	}
	if !response.OK() {
		return IndexMap{}, fmt.Errorf("collect: %s answered %s", opnsense.InterfacesInfo.Path, response.Outcome)
	}
	rows, err := decode.Rows(response.Body)
	if err != nil {
		return IndexMap{}, fmt.Errorf("collect: reading %s: %w", opnsense.InterfacesInfo.Path, err)
	}
	read := IndexMap{ByIndex: map[uint64]push.Interface{}}
	loopbacks := 0
	for _, row := range rows {
		device, hasDevice := decode.String(row, "device")
		statistics, hasStatistics := row["statistics"].(map[string]any)
		if !hasDevice || !hasStatistics {
			continue
		}
		index, ok := interfaceIndex(statistics["index"])
		if !ok {
			continue
		}
		identifier, _ := decode.String(row, "identifier")
		read.ByIndex[index] = push.Interface{Identifier: identifier, Device: device}
		if hasFlag(row["flags"], "loopback") {
			read.Loopback = index
			loopbacks++
		}
	}
	if loopbacks != 1 {
		read.Loopback = 0
	}
	return read, nil
}

// hasFlag reports whether a row's flags list holds a flag.
func hasFlag(value any, flag string) bool {
	flags, _ := value.([]any)
	for _, candidate := range flags {
		if text, ok := candidate.(string); ok && text == flag {
			return true
		}
	}
	return false
}

// interfaceIndex reads `index`, which ifinfo prints and legacy_interface_stats keeps as text.
func interfaceIndex(value any) (uint64, bool) {
	switch typed := value.(type) {
	case string:
		index, err := strconv.ParseUint(typed, 10, 32)
		return index, err == nil && index > 0
	case float64:
		return uint64(typed), typed > 0 && typed == float64(uint64(typed))
	}
	return 0, false
}
