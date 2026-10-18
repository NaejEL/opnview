package messages

import (
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/push"
	// The push protocols register themselves, and with them the gap reasons they declare.
	_ "github.com/NaejEL/opnview/internal/push/protocols"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The registry's vocabulary: the kinds and providers the provider table registers, and the
// reasons a collection gap records. A key is composed from a registry row (KindLabelKey,
// ProviderLabelKey, GapReasonKey) by the surfaces that name a source.
//
// NO KIND, PROVIDER OR PROTOCOL IS NAMED HERE. The kind keys come from the core kind files
// (internal/collect/provider/kinds), the registered providers' keys from those files' `registered`
// entries, and the gap reasons from the core's own detections (vocab) and from what each push
// protocol declares (push.GapReasoner). A plugin's own kinds and providers carry their keys in its
// locale. Adding a kind or a protocol is therefore adding its file, and its catalogue entry.

const (
	msgLabelGapReason          Key = "collection.label.gap_reason"
	msgGroupImplementations    Key = "collection.group.implementations"
	msgPageNotAdjustable       Key = "state.sizing.page_not_adjustable"
	msgLabelRetentionNow       Key = "collection.label.retention_now"
	msgLabelRetentionRequested Key = "collection.label.retention_requested"
	msgEditedNotSaved          Key = "notice.edited_not_saved"
	msgSecretNotKept           Key = "state.secret_not_kept"
	msgOpenInOPNsense          Key = "sources.label.opnsense_page"
)

// coreGapReasons are the gap reasons the core itself detects, matching the schema's CHECK beside
// the protocols' own.
var coreGapReasons = []vocab.GapReason{
	vocab.GapDigestOutsideWindow, vocab.GapEveRotationLost, vocab.GapResolverWindowNotHonoured,
	vocab.GapDownloadFailed, vocab.GapPushBufferOverflow, vocab.GapPushStoreFailed, vocab.GapPushListenerDown,
	vocab.GapPushScriptFailed, vocab.GapPushNotAlive,
}

// vocabularyKeys is every key above, and every key composed from the kind files and the protocols.
func vocabularyKeys() []Key {
	keys := []Key{
		msgLabelGapReason, msgGroupImplementations, msgPageNotAdjustable, msgLabelRetentionNow,
		msgLabelRetentionRequested, msgEditedNotSaved, msgSecretNotKept, msgOpenInOPNsense,
	}
	for _, contract := range provider.Kinds() {
		keys = append(keys, KindLabelKey(contract.Kind))
		for _, registered := range contract.Registered {
			keys = append(keys, ProviderLabelKey(contract.Kind, registered.Key))
		}
	}
	for _, reason := range coreGapReasons {
		keys = append(keys, GapReasonKey(string(reason)))
	}
	for _, declared := range push.DeclaredGapReasons() {
		keys = append(keys, GapReasonKey(declared.Reason))
	}
	return keys
}
