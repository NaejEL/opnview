package traffic

import (
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// volumeRow is one row of the rolling window as a script hands it back: the rows the figures
// library's volume area assembles (plugins/figures, ADR 0021 step 4), read here by the peers area
// until it moves as well.
type volumeRow struct {
	SrcInterfaceID   *int64  `json:"src_interface_id"`
	DstInterfaceID   *int64  `json:"dst_interface_id"`
	PeerAddress      *string `json:"peer_address"`
	TrafficScope     string  `json:"traffic_scope"`
	TrafficDirection string  `json:"traffic_direction"`
	ExitLegDevice    *string `json:"exit_leg_device"`
	traffic.SummableFigures
}

// key is a row's key as the core reads it.
func (row volumeRow) key() reads.VolumeKey {
	return reads.VolumeKey{SrcInterfaceID: row.SrcInterfaceID, DstInterfaceID: row.DstInterfaceID,
		PeerAddress: row.PeerAddress, TrafficScope: row.TrafficScope, TrafficDirection: row.TrafficDirection,
		ExitLegDevice: row.ExitLegDevice}
}
