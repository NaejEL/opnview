package store_test

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The tests of the rule that replaced one-active-provider-per-kind.
//
// The rule: A KIND ADMITS SEVERAL CONCURRENTLY ACTIVE PROVIDERS EXACTLY WHEN THE IDENTITY OF ITS
// DESTINATION ROWS INCLUDES THE PROVIDER. It is written out beside the index that enforces it in
// schema.sql and mirrored in internal/store/vocab/kinds.go, and these tests are what keep the two
// together and what make both directions of the rule fail loudly when broken.

// TestTwoSecurityEventProvidersCanBothBeActive is the case that made the old index wrong.
//
// People run Suricata, CrowdSec and Zenarmor together, and the how-to corpus is people stacking
// them, so three concurrent security_event providers is the normal installation rather than the
// exotic one. Event identity is (provider_id, provider_event_key), so two sources reporting one
// intrusion are two attributed rows and no figure is doubled.
func TestTwoSecurityEventProvidersCanBothBeActive(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	// A second implementation of the kind is an INSERT and never a schema change.
	if _, err := database.DB().ExecContext(ctx,
		`INSERT INTO provider (kind, provider_key, display_name, is_active, registered_at)
		 VALUES ('security_event', 'example-second-engine', 'Example second engine', false, 1750000000)`,
	); err != nil {
		t.Fatalf("registering a second security-event provider: %v", err)
	}

	if err := database.SetActiveProviders(ctx, "security_event",
		"suricata", "example-second-engine"); err != nil {
		t.Fatalf("activating two security-event providers: %v", err)
	}

	ids, err := database.ActiveProviderIDs(ctx, "security_event")
	if err != nil {
		t.Fatalf("reading the active security-event providers: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("%d security-event providers are active, want both", len(ids))
	}

	// And both stay active: neither activation silently replaced the other, which is exactly
	// what the dropped index used to do.
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		key, err := database.ProviderKey(ctx, id)
		if err != nil {
			t.Fatalf("reading an active provider's key: %v", err)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "example-second-engine,suricata" {
		t.Fatalf("the active security-event providers are %v", keys)
	}

	// Two events, one per provider, on the same provider_event_key. They are two rows because
	// the identity includes the provider — which is the whole reason this kind may have two
	// active providers at once.
	for _, id := range ids {
		if err := database.InsertSecurityEvent(ctx, id, vocab.SecurityEvent{
			ProviderEventKey: "example-shared-event-key",
			OccurredAt:       1750000000, IngestedAt: 1750000000,
			RuleIdentity: "example-rule-identity", Signature: "example signature",
			EventAction: "blocked",
			SrcAddress:  "example-source", DstAddress: "example-destination",
		}); err != nil {
			t.Fatalf("writing an event from provider %d: %v", id, err)
		}
	}
	if stored := count(t, database, "security_event"); stored != 2 {
		t.Fatalf("one event key under two providers stored %d rows, want 2", stored)
	}
}

// TestAskingForTheSingleActiveProviderOfAConcurrentKindIsRefused is the other half of the same
// decision, and it is the half that would otherwise fail silently.
//
// A caller that asks for THE active provider of a kind three engines are feeding would read one
// source and report its figures as the whole picture. The accessor refuses instead, and names
// the plural one.
func TestAskingForTheSingleActiveProviderOfAConcurrentKindIsRefused(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	for _, kind := range vocab.ConcurrentKinds() {
		if _, _, err := database.ActiveProviderID(ctx, kind); err == nil {
			t.Errorf("asking for the single active provider of %s succeeded, and that kind "+
				"admits several", kind)
		}
	}
	// And an exclusive kind answers, because it has a single answer to give.
	if _, _, err := database.ActiveProviderID(ctx, "dns_lookup"); err != nil {
		t.Errorf("asking for the single active dns_lookup provider failed: %v", err)
	}
}

// TestActivatingSeveralProvidersOfAnExclusiveKindIsRefused is exclusivity where it still holds.
//
// The kind it uses MOVED: dhcp_lease is concurrent now, because one server on one VLAN and
// another on a second is an ordinary deployment. dns_lookup is not, and for a reason that is
// about the rows rather than about the deployment — dns_resolution.lookup_key carries no
// provider, so a lookup that transited two resolvers would be two records nothing could tell
// apart from two lookups.
//
// The index would reject it anyway; refusing here gives the caller a sentence instead of a
// constraint violation, and says which of the two guarantees it broke.
func TestActivatingSeveralProvidersOfAnExclusiveKindIsRefused(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	err := database.SetActiveProviders(context.Background(), "dns_lookup", "unbound", "dnsmasq")
	if err == nil {
		t.Fatal("two dns_lookup providers were activated at once")
	}
	// The partial index is still there for the kinds it governs, and it is what makes the
	// refusal above a belt-and-braces check rather than the only guard.
	if _, directErr := database.DB().ExecContext(context.Background(),
		"UPDATE provider SET is_active = true WHERE kind = 'dns_lookup'"); directErr == nil {
		t.Fatal("a direct statement activated every dns_lookup provider, so the index that " +
			"replaced universal exclusivity does not cover the exclusive kinds")
	}
}

// TestTheConcurrentKindsInCodeAreTheOnesTheSchemaExempts keeps the rule from having two truths.
//
// internal/store/vocab/kinds.go exists so the probe round can activate every qualifying provider of
// a concurrent kind instead of discovering the constraint by failing. The database is the
// authority, so the set in code is read back out of the index's own definition and compared.
func TestTheConcurrentKindsInCodeAreTheOnesTheSchemaExempts(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)

	var definition string
	if err := database.DB().QueryRowContext(context.Background(),
		`SELECT pg_get_indexdef('uq_provider_active_per_exclusive_kind'::regclass)`).
		Scan(&definition); err != nil {
		t.Fatalf("reading the exclusivity index out of the schema: %v", err)
	}

	for _, kind := range vocab.ConcurrentKinds() {
		if !strings.Contains(definition, "'"+kind+"'") {
			t.Errorf("code says the %s kind admits several active providers and the index does "+
				"not exempt it, so the database would reject the second one", kind)
		}
	}

	// And the other direction: every kind the index exempts is one the code says admits several
	// active providers. The exempt kinds are read out of the index's own definition rather than
	// from a list here.
	for _, quoted := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(definition, -1) {
		if !vocab.KindAdmitsSeveralActiveProviders(quoted[1]) {
			t.Errorf("the index exempts %s and the code says it admits one active provider", quoted[1])
		}
	}
}

// TestTheMeasurementSampleKindHasARegistryRowAndAnAvailabilityRow is the promotion itself.
//
// measurement_sample was a table with no kind: no provider_key, no availability row, no place in
// provider.kind, so none of the eight surveyed sources that fit its shape had anywhere to
// announce itself. All three exist now.
func TestTheMeasurementSampleKindHasARegistryRowAndAnAvailabilityRow(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()

	providerID, err := database.ProviderID(ctx, "measurement_sample", "insight")
	if err != nil {
		t.Fatalf("the measurement_sample kind has no registry row: %v", err)
	}
	state, probe, _, err := database.Availability(ctx, providerID)
	if err != nil {
		t.Fatalf("the measurement_sample provider has no availability row: %v", err)
	}
	if state != vocab.StateUnavailable || probe != "not_yet_probed" {
		t.Errorf("a freshly applied schema reports the measurement provider as %q by %q, want "+
			"the not-yet-probed unavailable state", state, probe)
	}
}
