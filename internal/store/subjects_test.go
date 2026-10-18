package store_test

import (
	"strings"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The subjects the tests sample, keyed as the insight plugin keys them -- the device and the
// addresses joined by spaces, local end first -- with their parts beside the key, as a provider
// reports them. The store reads the parts and never the key.

func endpointKey(device, local string) string { return device + " " + local }

func pairKey(device, local, peer string) string { return device + " " + local + " " + peer }

// withParts fills a reading's parts from its key, for a subject read on one interface.
func withParts(sample vocab.MeasurementSample) vocab.MeasurementSample {
	parts := strings.Split(sample.SubjectKey, " ")
	switch {
	case sample.SubjectKind == vocab.SubjectInterfaceEndpoint && len(parts) == 2,
		sample.SubjectKind == vocab.SubjectInterfaceEndpointPair && len(parts) == 3:
		sample.InterfaceDevice, sample.LocalAddress = &parts[0], &parts[1]
		if len(parts) == 3 {
			sample.PeerAddress = &parts[2]
		}
	}
	return sample
}
