package derive

import (
	"context"
	"fmt"
	"strconv"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/push"
)

// pushIndexReader is the reader the broker records the interface-index read under.
const pushIndexReader = "push"

// InterfaceIndexes maps a NetFlow exporter's interface indexes to the firewall's interfaces (PS11 of
// the push amendment): a read through the broker, at discovery's max age, of
// GET /api/interfaces/overview/interfaces_info/1, whose `details` argument adds to each row its
// `statistics`, the output of OPNsense's `ifinfo` per device, whose `index` is the kernel interface
// index ng_netflow exports (opnsense/core 26.7.3, Interfaces/Api/OverviewController.php parseIfInfo
// and src/etc/inc/interfaces.lib.inc legacy_interface_stats; survey, Push sources, S6). OPNsense's own
// NetFlow aggregation maps an index the same way (src/opnsense/scripts/netflow/lib/parse.py). A row
// carrying no index maps nothing: an index is never matched by position, name or count.
func (c *Deriver) InterfaceIndexes(ctx context.Context) (map[uint64]push.Interface, error) {
	response, err := c.Read(ctx, pushIndexReader, opnsense.InterfacesInfo,
		opnsense.RequestOptions{Arguments: []string{"1"}}, c.DiscoveryMaxAge())
	if err != nil {
		return nil, err
	}
	if !response.OK() {
		return nil, fmt.Errorf("collect: %s answered %s", opnsense.InterfacesInfo.Path, response.Outcome)
	}
	rows, err := decode.Rows(response.Body)
	if err != nil {
		return nil, fmt.Errorf("collect: reading %s: %w", opnsense.InterfacesInfo.Path, err)
	}
	indexes := map[uint64]push.Interface{}
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
		indexes[index] = push.Interface{Identifier: identifier, Device: device}
	}
	return indexes, nil
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
