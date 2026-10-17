package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/netip"
	"strconv"
)

// The flow_record kind's rule for a pushed record: what its types cannot express, and its identity.
//
// THE IDENTITY IS THE CORE'S, composed from the delivery the record came in: the exporter's address,
// the observation domain, the delivery (one datagram's octets) and the record's position in it, so a
// datagram delivered twice stores its records once; two records of one datagram, identical in every
// field, keep their two positions and are two rows; a record after the exporter's sequence restarted,
// and the records of two counters sharing one observation domain with the same sequence number and
// export time, come in datagrams whose octets differ and never collide. The one case the identity
// cannot separate is two datagrams identical octet for octet: they are one delivery.
//
// An address is read in its canonical form; the version an exporter does not send is the addresses'
// own, and one it sends must agree with them; the last packet is never before the first.

func init() {
	RegisterPushConversion("flow_record", PushConversion{Normalise: normaliseFlowRecord})
}

// normaliseFlowRecord converts one checked flow_record record.
func normaliseFlowRecord(checked Answer, delivery PushDelivery) (Answer, bool) {
	if delivery.Delivery == "" {
		return nil, false
	}
	source, sourceErr := netip.ParseAddr(Text(checked, "src_address"))
	destination, destinationErr := netip.ParseAddr(Text(checked, "dst_address"))
	exporter, exporterErr := netip.ParseAddr(Text(checked, "exporter_address"))
	if sourceErr != nil || destinationErr != nil || exporterErr != nil {
		return nil, false
	}
	family := func(address netip.Addr) int64 {
		if address.Unmap().Is4() {
			return 4
		}
		return 6
	}
	version, sent := checked["ip_version"].(int64)
	if !sent {
		version = family(source)
	}
	if version != family(source) || family(destination) != version {
		return nil, false
	}
	start, end := FlowRecordMillis(Number(checked, "start_at")), FlowRecordMillis(Number(checked, "end_at"))
	if end < start {
		return nil, false
	}
	normalised := Answer{}
	for name, value := range checked {
		normalised[name] = value
	}
	normalised["exporter_address"] = exporter.Unmap().String()
	normalised["src_address"] = source.Unmap().String()
	normalised["dst_address"] = destination.Unmap().String()
	normalised["ip_version"] = version
	normalised["received_at"] = math.Floor(Number(checked, "received_at"))
	normalised["start_at"], normalised["end_at"] = float64(start)/1000, float64(end)/1000
	sum := sha256.Sum256([]byte(Text(normalised, "exporter_address") + "|" +
		strconv.FormatInt(Integer(checked, "observation_domain"), 10) + "|" + delivery.Delivery + "|" +
		strconv.Itoa(delivery.Position)))
	normalised["identity"] = hex.EncodeToString(sum[:])
	return normalised, true
}

// FlowRecordMillis turns epoch seconds with a fraction into epoch milliseconds, rounding the fraction
// the float cannot hold exactly: the precision the decoder carries, of which the exporter may fill only
// whole seconds.
func FlowRecordMillis(seconds float64) int64 {
	return int64(math.Round(seconds * 1000))
}
