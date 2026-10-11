package interfaces

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/web"
)

// What the external tests of this surface need of its internals.

// NotCountingReason is the word beside a "no" that says why a network does not count.
var NotCountingReason = notCountingReason

// OriginKey names where a network came from.
var OriginKey = originKey

// PageFigures are every figure the Interfaces page draws, per interface id, read through the
// page's own fill: what the JSON answer must carry (C-AC9). A state is its catalogue key.
func PageFigures(ctx context.Context, host web.Host) (map[string][]string, error) {
	var built view
	if err := newSurface(host).fillInterfaces(ctx, &built, nil); err != nil {
		return nil, err
	}
	figures := map[string][]string{}
	for _, card := range built.Interfaces {
		lines := []string{"description " + card.Description, "identifier " + card.Identifier,
			"device " + card.Device, "upstream " + string(card.UpstreamKey)}
		for _, option := range card.LinkLocal {
			if option.Selected {
				lines = append(lines, "link_local "+option.Value)
			}
		}
		for _, network := range card.Networks {
			lines = append(lines, fmt.Sprintf("network %s %s removed %s counts %s reason %s", network.Network,
				network.OriginKey, network.RemovedKey, network.CountsKey, network.ReasonKey))
		}
		for _, overlap := range card.Overlaps {
			lines = append(lines, fmt.Sprintf("overlap %s %s %s", overlap.Network, overlap.Other, overlap.Description))
		}
		figures[card.ID] = lines
	}
	return figures, nil
}
