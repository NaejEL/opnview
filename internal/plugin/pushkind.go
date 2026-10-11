package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/push"
)

// A push entry delivering records of a core kind (PS9 of the push amendment): its parse function
// builds records of the kind's contract (contract.go), checked as a pulled answer is, and they go to
// the kind's coordinator, which keeps one path per instant (internal/collect, pushpaths.go).

// KindReceiver is the core's side: it takes the records a push entry delivered for one provider.
type KindReceiver interface {
	ReceivePushedFirewallLog(ctx context.Context, key string, records []firewalllog.Record) error
}

// pushKindAdapters are the core kinds a push entry may deliver, each with how its records are
// checked and converted. A kind absent here is refused at load.
var pushKindAdapters = map[string]func(answers []map[string]any, records []push.Record) (any, int, error){
	firewalllog.Kind: firewallLogFromPush,
}

// firewallLogFromPush checks a batch of firewall_log records against the contract, and gives each the
// pushed identity: a digest the core computes from the pushed message under a prefix no pulled
// digest carries, whatever the script said, so the two paths' identities can never meet.
func firewallLogFromPush(answers []map[string]any, records []push.Record) (any, int, error) {
	contract, _ := provider.ContractOf(firewalllog.Kind)
	var converted []firewalllog.Record
	invalid := 0
	for index, answer := range answers {
		answer["digest"] = pushedDigest(records[index])
		checked, err := checkRecord(contract, "firewall_log_record", answer, fmt.Sprintf("record %d", index))
		if err != nil {
			invalid++
			continue
		}
		record := firewallLogRecord(checked)
		if firewallLogRecordsInvalid([]firewalllog.Record{record}) != "" {
			invalid++
			continue
		}
		converted = append(converted, record)
	}
	return converted, invalid, nil
}

// pushedDigest is a pushed record's identity: the push prefix and the SHA-256 of what the message
// said and who said it -- header and message -- so the same line delivered twice is stored once.
func pushedDigest(record push.Record) string {
	var text strings.Builder
	for _, name := range sortedKeys(record.Fields) {
		fmt.Fprintf(&text, "%s=%v\x00", name, record.Fields[name])
	}
	sum := sha256.Sum256([]byte(text.String()))
	return pushedDigestPrefix + hex.EncodeToString(sum[:])
}

// pushedDigestPrefix is internal/collect's PushedDigestPrefix, which the collector reads the newest
// pushed record by; the plugin package cannot import the collector, so a test holds them equal.
const pushedDigestPrefix = "push:"

// kindSink hands a push entry's records to the core kind's coordinator.
type kindSink struct {
	binding     *pushBinding
	plugin      *Plugin
	environment PushEnvironment
}

// kindBatch is what Prepare made: the converted records.
type kindBatch struct {
	records any
}

func (s *kindSink) Prepare(ctx context.Context, records []push.Record) push.Prepared {
	prepared := push.Prepared{Counts: map[string]int64{}}
	convert := pushKindAdapters[s.binding.declaration.Kind]
	var all []firewalllog.Record
	for start := 0; start < len(records); start += ParseBatch {
		chunk := records[start:min(len(records), start+ParseBatch)]
		answers, failed := callParse(ctx, s.plugin, s.environment.Session, s.binding.declaration.Parse, chunk,
			prepared.Counts)
		if failed != "" {
			prepared.Failed += len(chunk)
			prepared.FailedReason = push.GapScriptFailed
			continue
		}
		var objects []map[string]any
		var sources []push.Record
		for index, answer := range answers {
			if answer == nil {
				prepared.Counts[pushCounterSkipped]++
				continue
			}
			object, isObject := answer.(map[string]any)
			if !isObject {
				prepared.Counts[pushCounterRecordsInvalid]++
				continue
			}
			objects = append(objects, object)
			sources = append(sources, chunk[index])
		}
		converted, invalid, _ := convert(objects, sources)
		prepared.Counts[pushCounterRecordsInvalid] += int64(invalid)
		if typed, ok := converted.([]firewalllog.Record); ok {
			all = append(all, typed...)
		}
	}
	prepared.Rows = len(all)
	if len(all) > 0 {
		prepared.Payload = kindBatch{records: all}
	}
	return prepared
}

func (s *kindSink) Store(ctx context.Context, prepared push.Prepared) error {
	batch, ok := prepared.Payload.(kindBatch)
	if !ok {
		return nil
	}
	records, _ := batch.records.([]firewalllog.Record)
	return s.environment.Kinds.ReceivePushedFirewallLog(ctx, s.binding.providerKey, records)
}
