package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/owners"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The tests of C1 of specs/SPEC-backend-completion-plugins.md at the store: people, assignments,
// the client listing, labels and purposes. Every value below is an example a test is allowed to
// hold; the code under test names its rows by id and matches on none of them.

// refusalCode is the closed code of a refused write, or "" when err is not a refusal.
func refusalCode(err error) string {
	var refusal *owners.UserInputRefusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return ""
}

// ownerRows is the owner table as text, so a test can assert that a refusal changed no row.
func ownerRows(t *testing.T, database *Store) string {
	t.Helper()
	var text string
	if err := database.DB().QueryRowContext(context.Background(),
		`SELECT coalesce(string_agg(id || '|' || display_name || '|' || updated_at, ',' ORDER BY id), '')
		 FROM owner`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

// clientOwnership is every client's owner and instant, as text.
func clientOwnership(t *testing.T, database *Store) string {
	t.Helper()
	var text string
	if err := database.DB().QueryRowContext(context.Background(),
		`SELECT coalesce(string_agg(id || '|' || coalesce(CAST(owner_id AS TEXT), '-') || '|' ||
		        coalesce(CAST(owner_assigned_at AS TEXT), '-'), ',' ORDER BY id), '') FROM client`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

// exampleClient writes one client by MAC and returns its id.
func exampleClient(t *testing.T, database *Store, mac, hostname, vendor string, at int64) int64 {
	t.Helper()
	client := vocab.Client{Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: mac}, MAC: &mac}
	if hostname != "" {
		client.Hostname = &hostname
	}
	if vendor != "" {
		client.VendorHint = &vendor
	}
	id, err := database.UpsertClient(context.Background(), client, at)
	if err != nil {
		t.Fatalf("writing a client: %v", err)
	}
	return id
}

// TestCreatingAPersonRefusesAnEmptyOrDuplicateNameAndChangesNoRow is C-AC1, create: a name is
// trimmed, and an empty or taken one is refused with its closed code while no row changes.
func TestCreatingAPersonRefusesAnEmptyOrDuplicateNameAndChangesNoRow(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	created, err := database.CreateOwner(ctx, "  Example Person  ", 1000)
	if err != nil {
		t.Fatalf("creating a person: %v", err)
	}
	if created.DisplayName != "Example Person" || created.ID == 0 || created.CreatedAt != 1000 {
		t.Errorf("the person was created as %+v", created)
	}
	before := ownerRows(t, database)
	for raw, code := range map[string]string{
		"":                  owners.RefusalDisplayNameEmpty,
		"   \t ":            owners.RefusalDisplayNameEmpty,
		"Example Person":    owners.RefusalDisplayNameTaken,
		" Example Person\n": owners.RefusalDisplayNameTaken,
	} {
		if _, err := database.CreateOwner(ctx, raw, 2000); refusalCode(err) != code {
			t.Errorf("creating %q answered %v, not %s", raw, err, code)
		}
	}
	if after := ownerRows(t, database); after != before {
		t.Errorf("a refused creation changed the owner table: %s -> %s", before, after)
	}
	// The schema's UNIQUE compares bytes: another case is another name.
	if _, err := database.CreateOwner(ctx, "example person", 3000); err != nil {
		t.Errorf("a name differing in case was refused: %v", err)
	}
	owners, err := database.Owners(ctx)
	if err != nil || len(owners) != 2 {
		t.Fatalf("the listing holds %v (%v)", owners, err)
	}
}

// TestRenamingAPersonFollowsTheCreationRules is C-AC1, rename.
func TestRenamingAPersonFollowsTheCreationRules(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	first, err := database.CreateOwner(ctx, "Example Person A", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateOwner(ctx, "Example Person B", 1000); err != nil {
		t.Fatal(err)
	}
	before := ownerRows(t, database)
	for raw, code := range map[string]string{" ": owners.RefusalDisplayNameEmpty, "Example Person B": owners.RefusalDisplayNameTaken} {
		if _, err := database.RenameOwner(ctx, first.ID, raw, 2000); refusalCode(err) != code {
			t.Errorf("renaming to %q answered %v, not %s", raw, err, code)
		}
	}
	if _, err := database.RenameOwner(ctx, first.ID+100, "Anything", 2000); refusalCode(err) != owners.RefusalUnknownOwner {
		t.Errorf("renaming nobody answered %v", err)
	}
	if after := ownerRows(t, database); after != before {
		t.Errorf("a refused rename changed the owner table: %s -> %s", before, after)
	}
	renamed, err := database.RenameOwner(ctx, first.ID, " Example Person C ", 2000)
	if err != nil || renamed.DisplayName != "Example Person C" || renamed.UpdatedAt != 2000 {
		t.Errorf("the rename answered %+v (%v)", renamed, err)
	}
}

// TestDeletingAPersonReturnsEveryClearedAssignment is C-AC1, delete: every assignment is cleared
// and returned with its instant, and the client rows themselves stay.
func TestDeletingAPersonReturnsEveryClearedAssignment(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	person, err := database.CreateOwner(ctx, "Example Person", 1000)
	if err != nil {
		t.Fatal(err)
	}
	first := exampleClient(t, database, "00:00:5e:00:53:01", "", "", 1000)
	second := exampleClient(t, database, "00:00:5e:00:53:02", "", "", 1000)
	other := exampleClient(t, database, "00:00:5e:00:53:03", "", "", 1000)
	if _, err := database.AssignClients(ctx, person.ID, []int64{first}, 1100); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AssignClients(ctx, person.ID, []int64{second}, 1200); err != nil {
		t.Fatal(err)
	}
	deleted, err := database.DeleteOwner(ctx, person.ID, 1300)
	if err != nil {
		t.Fatalf("deleting the person: %v", err)
	}
	want := []owners.Assignment{{ClientID: first, OwnerAssignedAt: 1100}, {ClientID: second, OwnerAssignedAt: 1200}}
	if fmt.Sprint(deleted.Cleared) != fmt.Sprint(want) || deleted.DisplayName != "Example Person" {
		t.Errorf("the deletion returned %+v, want the cleared %v", deleted, want)
	}
	if owners, _ := database.Owners(ctx); len(owners) != 0 {
		t.Errorf("the person is still listed: %v", owners)
	}
	if ownership := clientOwnership(t, database); ownership != fmt.Sprintf("%d|-|-,%d|-|-,%d|-|-", first, second, other) {
		t.Errorf("the clients stand as %s", ownership)
	}
	if _, err := database.DeleteOwner(ctx, person.ID, 1400); refusalCode(err) != owners.RefusalUnknownOwner {
		t.Errorf("deleting nobody answered %v", err)
	}
}

// TestUndoingADeletionRestoresTheOriginalAssignedAtAndReportsAClientAssignedElsewhere is C-AC1,
// undo.
func TestUndoingADeletionRestoresTheOriginalAssignedAtAndReportsAClientAssignedElsewhere(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	person, err := database.CreateOwner(ctx, "Example Person", 1000)
	if err != nil {
		t.Fatal(err)
	}
	someoneElse, err := database.CreateOwner(ctx, "Example Other", 1000)
	if err != nil {
		t.Fatal(err)
	}
	kept := exampleClient(t, database, "00:00:5e:00:53:11", "", "", 1000)
	taken := exampleClient(t, database, "00:00:5e:00:53:12", "", "", 1000)
	if _, err := database.AssignClients(ctx, person.ID, []int64{kept}, 1111); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AssignClients(ctx, person.ID, []int64{taken}, 1222); err != nil {
		t.Fatal(err)
	}
	deleted, err := database.DeleteOwner(ctx, person.ID, 2000)
	if err != nil {
		t.Fatal(err)
	}
	// In the meantime, one of the two is assigned to somebody else.
	if _, err := database.AssignClients(ctx, someoneElse.ID, []int64{taken}, 2500); err != nil {
		t.Fatal(err)
	}
	gone := owners.Assignment{ClientID: taken + 1000, OwnerAssignedAt: 900}
	restored, err := database.RestoreOwner(ctx, deleted.DisplayName, append(deleted.Cleared, gone), 3000)
	if err != nil {
		t.Fatalf("undoing the deletion: %v", err)
	}
	if restored.Owner.DisplayName != "Example Person" || len(restored.Restored) != 1 ||
		restored.Restored[0] != (owners.Assignment{ClientID: kept, OwnerAssignedAt: 1111}) {
		t.Errorf("the undo restored %+v", restored)
	}
	if len(restored.NotRestored) != 2 || restored.NotRestored[0].ClientID != taken ||
		restored.NotRestored[0].Reason != owners.NotRestoredAssignedElsewhere || *restored.NotRestored[0].OwnerID != someoneElse.ID ||
		restored.NotRestored[1].Reason != owners.NotRestoredClientGone {
		t.Errorf("the undo reported %+v", restored.NotRestored)
	}
	want := fmt.Sprintf("%d|%d|1111,%d|%d|2500", kept, restored.Owner.ID, taken, someoneElse.ID)
	if ownership := clientOwnership(t, database); ownership != want {
		t.Errorf("after the undo the clients stand as %s, want %s", ownership, want)
	}
	// An undo under a name somebody holds again is refused and writes nothing.
	before := clientOwnership(t, database) + ownerRows(t, database)
	if _, err := database.RestoreOwner(ctx, "Example Other", []owners.Assignment{{ClientID: kept, OwnerAssignedAt: 5}},
		4000); refusalCode(err) != owners.RefusalDisplayNameTaken {
		t.Errorf("an undo under a taken name answered %v", err)
	}
	if _, err := database.RestoreOwner(ctx, "Example New", []owners.Assignment{{ClientID: kept, OwnerAssignedAt: -1}},
		4000); refusalCode(err) != owners.RefusalInvalidInstant {
		t.Errorf("an undo with an instant out of range answered %v", err)
	}
	if after := clientOwnership(t, database) + ownerRows(t, database); after != before {
		t.Errorf("a refused undo changed rows: %s -> %s", before, after)
	}
}

// TestAssigningAClientNeverInfersAnotherClientsOwner is C-AC2 and C-AC1's assign and unassign: the
// fixture tempts a match -- the same hostname, the same vendor, the same MAC prefix -- and only the
// client named changes; a client with an unstable identity is assigned and listed as such; an
// unknown client refuses the whole write.
func TestAssigningAClientNeverInfersAnotherClientsOwner(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	person, err := database.CreateOwner(ctx, "Example Person", 1000)
	if err != nil {
		t.Fatal(err)
	}
	named := exampleClient(t, database, "00:00:5e:00:53:21", "laptop.example", "Example Vendor", 1000)
	exampleClient(t, database, "00:00:5e:00:53:22", "laptop.example", "Example Vendor", 1000)
	exampleClient(t, database, "00:00:5e:00:53:23", "phone.example", "Example Vendor", 1000)
	unstable := exampleClient(t, database, "02:00:5e:00:53:24", "laptop.example", "", 1000)

	changes, err := database.AssignClients(ctx, person.ID, []int64{named, unstable}, 1500)
	if err != nil {
		t.Fatalf("assigning: %v", err)
	}
	if len(changes) != 2 || !changes[0].Changed || !changes[1].Changed {
		t.Errorf("the assignment answered %+v", changes)
	}
	clients, err := database.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 4 {
		t.Fatalf("the listing holds %d clients, not 4", len(clients))
	}
	for _, client := range clients {
		assigned := client.ID == named || client.ID == unstable
		if assigned != (client.OwnerID != nil) {
			t.Errorf("client %d (%v) has the owner %v", client.ID, *client.Hostname, client.OwnerID)
		}
		if assigned && (*client.OwnerID != person.ID || *client.OwnerAssignedAt != 1500 ||
			*client.OwnerDisplayName != "Example Person") {
			t.Errorf("client %d is listed as %+v", client.ID, client)
		}
		if client.UnstableIdentity != (client.ID == unstable) {
			t.Errorf("client %d lists unstable_identity %v", client.ID, client.UnstableIdentity)
		}
	}

	// Assigning again to the same person keeps the original instant.
	again, err := database.AssignClients(ctx, person.ID, []int64{named}, 1600)
	if err != nil || again[0].Changed || *again[0].OwnerAssignedAt != 1500 {
		t.Errorf("a repeated assignment answered %+v (%v)", again, err)
	}
	// An unknown client, an empty list or a client named twice refuses the whole write.
	before := clientOwnership(t, database)
	for name, attempt := range map[string]func() error{
		owners.RefusalUnknownClient: func() error {
			_, err := database.AssignClients(ctx, person.ID, []int64{named + 1, unstable + 1000}, 1700)
			return err
		},
		owners.RefusalNoClients: func() error { _, err := database.AssignClients(ctx, person.ID, nil, 1700); return err },
		owners.RefusalDuplicateClient: func() error {
			_, err := database.UnassignClients(ctx, []int64{named, named}, 1700)
			return err
		},
		owners.RefusalUnknownOwner: func() error {
			_, err := database.AssignClients(ctx, person.ID+100, []int64{named + 1}, 1700)
			return err
		},
	} {
		if code := refusalCode(attempt()); code != name {
			t.Errorf("the write answered %q, want %s", code, name)
		}
	}
	if after := clientOwnership(t, database); after != before {
		t.Errorf("a refused write changed the clients: %s -> %s", before, after)
	}

	unassigned, err := database.UnassignClients(ctx, []int64{unstable}, 1800)
	if err != nil || !unassigned[0].Changed {
		t.Fatalf("unassigning answered %+v (%v)", unassigned, err)
	}
	if want := fmt.Sprintf("%d|%d|1500,%d|-|-,%d|-|-,%d|-|-", named, person.ID, named+1, named+2, unstable); clientOwnership(t, database) != want {
		t.Errorf("after the unassignment the clients stand as %s, want %s", clientOwnership(t, database), want)
	}
}

// TestTheClientListingCarriesEveryClientWithOrWithoutTraffic is C-AC1, the listing: a machine no
// flow ever named is listed beside one with traffic, with every field the listing carries.
func TestTheClientListingCarriesEveryClientWithOrWithoutTraffic(t *testing.T) {
	t.Parallel()
	network := populated(t, 2, 4, 40, 7200)
	ctx := context.Background()
	silent := exampleClient(t, network.db, "00:00:5e:00:53:31", "quiet.example", "", network.now-50)
	clients, err := network.db.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := queryInt(t, network.db, "SELECT count(*) FROM client")
	if int64(len(clients)) != count {
		t.Fatalf("the listing holds %d clients, the table %d", len(clients), count)
	}
	var found bool
	for _, client := range clients {
		if client.ID == silent {
			found = true
			if client.IdentityKind != "mac" || client.IdentityKey != "00:00:5e:00:53:31" || *client.MAC != client.IdentityKey ||
				*client.Hostname != "quiet.example" || client.LastSeenAt != network.now-50 || client.OwnerID != nil {
				t.Errorf("the silent client is listed as %+v", client)
			}
		}
		if client.InterfaceID != nil && (client.InterfaceIdentifier == nil || client.InterfaceDescription == nil) {
			t.Errorf("client %d names interface %d without its identifier", client.ID, *client.InterfaceID)
		}
	}
	if !found {
		t.Error("a client with no traffic is not listed")
	}
}

// TestAnAssignedClientSurvivesThePurgeAndAnUnassignedOneIsPurgedAsBefore is C-AC3.
func TestAnAssignedClientSurvivesThePurgeAndAnUnassignedOneIsPurgedAsBefore(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	now := int64(1750000000)
	if err := database.SetSetting(ctx, "retention_seconds", "3600", now); err != nil {
		t.Fatal(err)
	}
	old := now - 100000
	assigned := exampleClient(t, database, "00:00:5e:00:53:41", "", "", old)
	unassigned := exampleClient(t, database, "00:00:5e:00:53:42", "", "", old)
	person, err := database.CreateOwner(ctx, "Example Person", old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AssignClients(ctx, person.ID, []int64{assigned}, old+10); err != nil {
		t.Fatal(err)
	}
	if err := database.Purge(ctx, now); err != nil {
		t.Fatalf("purging: %v", err)
	}
	if queryInt(t, database, "SELECT count(*) FROM client WHERE id = ?", assigned) != 1 {
		t.Error("the purge removed a client somebody assigned to a person")
	}
	if queryInt(t, database, "SELECT count(*) FROM client WHERE id = ?", unassigned) != 0 {
		t.Error("the purge kept an unassigned client older than the horizon")
	}
	// Unassigned, it goes at the next purge like any other.
	if _, err := database.UnassignClients(ctx, []int64{assigned}, now); err != nil {
		t.Fatal(err)
	}
	if err := database.Purge(ctx, now+1); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, database, "SELECT count(*) FROM client WHERE id = ?", assigned) != 0 {
		t.Error("an unassigned client older than the horizon survived the purge")
	}
}

// ownerFamilyBytes sums one owner's bytes, or the unassigned bucket's for a nil owner, over a
// period's slots.
func ownerFamilyBytes(t *testing.T, database *Store, period string, owner *int64) int64 {
	t.Helper()
	if owner == nil {
		return queryInt(t, database, "SELECT coalesce(sum(bytes), 0) FROM owner_volume_aggregate_"+period+
			" WHERE owner_id IS NULL")
	}
	return queryInt(t, database, "SELECT coalesce(sum(bytes), 0) FROM owner_volume_aggregate_"+period+
		" WHERE owner_id = ?", *owner)
}

// TestAReassignmentForcesTheOwnerFamilyToBeRecomputed is C1's rule 3 at the store: a change of
// ownership records the hours holding the client's traffic, the next refresh rewrites them in
// every period and forgets exactly them, and the owner family then holds the client's bytes under
// its new owner while owners plus unassigned sum to the same total. A deletion moves the bytes to
// the unassigned bucket the same way.
func TestAReassignmentForcesTheOwnerFamilyToBeRecomputed(t *testing.T) {
	t.Parallel()
	network := populated(t, 2, 4, 200, 3*86400)
	ctx := context.Background()
	database := network.db
	first, err := database.CreateOwner(ctx, "Example Person A", network.now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateOwner(ctx, "Example Person B", network.now)
	if err != nil {
		t.Fatal(err)
	}
	client := queryInt(t, database, `SELECT client_id FROM client_volume_aggregate_1h
		GROUP BY client_id ORDER BY count(*) DESC, client_id LIMIT 1`)
	clientBytes := queryInt(t, database, "SELECT sum(bytes) FROM client_volume_aggregate_30d WHERE client_id = ?", client)
	totals := map[string]int64{}
	for _, period := range vocab.Periods() {
		totals[period.Name] = queryInt(t, database, "SELECT coalesce(sum(bytes), 0) FROM owner_volume_aggregate_"+period.Name)
	}
	refresh := func() {
		t.Helper()
		if _, err := database.RefreshAggregates(ctx, network.now, network.now, nil); err != nil {
			t.Fatalf("refreshing: %v", err)
		}
		if pending := queryInt(t, database, "SELECT count(*) FROM owner_reassigned_hour"); pending != 0 {
			t.Errorf("%d recorded hours survive the refresh that rewrote them", pending)
		}
		for _, period := range vocab.Periods() {
			if got := queryInt(t, database, "SELECT coalesce(sum(bytes), 0) FROM owner_volume_aggregate_"+period.Name); got != totals[period.Name] {
				t.Errorf("the %s owner family sums %d bytes, %d before", period.Name, got, totals[period.Name])
			}
		}
	}

	for step, owner := range []*int64{&first.ID, &second.ID, nil} {
		var err error
		if owner == nil {
			_, err = database.DeleteOwner(ctx, second.ID, network.now)
		} else {
			_, err = database.AssignClients(ctx, *owner, []int64{client}, network.now)
		}
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		hours := queryInt(t, database, "SELECT count(DISTINCT hour_start_at) FROM owner_reassigned_hour")
		if hours == 0 {
			t.Fatalf("step %d recorded no hour for the refresh", step)
		}
		refresh()
		for _, period := range vocab.Periods() {
			if got := ownerFamilyBytes(t, database, period.Name, owner); owner != nil && got != clientBytes {
				t.Errorf("step %d: the %s owner family holds %d bytes for the new owner, not the client's %d",
					step, period.Name, got, clientBytes)
			}
			if owner == nil && ownerFamilyBytes(t, database, period.Name, &second.ID) != 0 {
				t.Errorf("step %d: the deleted person still holds bytes in the %s family", step, period.Name)
			}
		}
		if owner != nil && *owner == second.ID && ownerFamilyBytes(t, database, "1h", &first.ID) != 0 {
			t.Errorf("the previous owner still holds the client's bytes")
		}
	}
	if violations := foreignKeyViolations(t, database); violations != 0 {
		t.Errorf("%d rows name a row that does not exist", violations)
	}
}

// TestALabelIsSetTrimmedAndClearedAndAnUnknownInterfaceIsRefused is C1's labels.
func TestALabelIsSetTrimmedAndClearedAndAnUnknownInterfaceIsRefused(t *testing.T) {
	t.Parallel()
	network := newTestNetwork(t, 1, 0)
	ctx := context.Background()
	id := network.inside[0].id
	label := "  Example label "
	labelled, err := network.db.SetInterfaceUserLabel(ctx, id, &label)
	if err != nil || labelled.UserLabel == nil || *labelled.UserLabel != "Example label" || labelled.ID != id {
		t.Fatalf("labelling answered %+v (%v)", labelled, err)
	}
	empty := " "
	if _, err := network.db.SetInterfaceUserLabel(ctx, id, &empty); refusalCode(err) != owners.RefusalLabelEmpty {
		t.Errorf("an empty label answered %v", err)
	}
	if _, err := network.db.SetInterfaceUserLabel(ctx, id+1000, &label); refusalCode(err) != owners.RefusalUnknownInterface {
		t.Errorf("labelling no interface answered %v", err)
	}
	cleared, err := network.db.SetInterfaceUserLabel(ctx, id, nil)
	if err != nil || cleared.UserLabel != nil {
		t.Errorf("clearing answered %+v (%v)", cleared, err)
	}
}

// TestAPurposeOutsideTheVocabularyIsRefusedAndTheVocabularyIsTheSchemas is C1's purposes: the
// vocabulary the code accepts is exactly the schema's CHECK, each value is stored with its instant,
// a value outside it is refused with a closed code, and nil clears both columns.
func TestAPurposeOutsideTheVocabularyIsRefusedAndTheVocabularyIsTheSchemas(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	var definition string
	if err := database.DB().QueryRowContext(ctx, `SELECT pg_get_constraintdef(c.oid)
		FROM pg_constraint AS c WHERE c.conrelid = 'blocklist'::regclass AND c.contype = 'c'
		  AND pg_get_constraintdef(c.oid) LIKE '%purpose = ANY%'`).Scan(&definition); err != nil {
		t.Fatalf("reading the purpose constraint: %v", err)
	}
	if count := strings.Count(definition, "'::text"); count != len(owners.BlocklistPurposes) {
		t.Errorf("the schema lists %d purposes, the code %d: %s", count, len(owners.BlocklistPurposes), definition)
	}
	id, err := database.UpsertBlocklist(ctx, "an observed list name", 100)
	if err != nil {
		t.Fatal(err)
	}
	for index, purpose := range owners.BlocklistPurposes {
		if !strings.Contains(definition, "'"+purpose+"'") {
			t.Errorf("the schema does not list %s", purpose)
		}
		value := purpose
		list, err := database.SetBlocklistPurpose(ctx, id, &value, int64(1000+index))
		if err != nil || *list.Purpose != purpose || *list.PurposeAssignedAt != int64(1000+index) {
			t.Errorf("assigning %s answered %+v (%v)", purpose, list, err)
		}
	}
	outside := "malware"
	if _, err := database.SetBlocklistPurpose(ctx, id, &outside, 2000); refusalCode(err) != owners.RefusalPurposeNotInVocabulary {
		t.Errorf("a purpose outside the vocabulary answered %v", err)
	}
	if _, err := database.SetBlocklistPurpose(ctx, id+100, nil, 2000); refusalCode(err) != owners.RefusalUnknownBlocklist {
		t.Errorf("no list answered %v", err)
	}
	cleared, err := database.SetBlocklistPurpose(ctx, id, nil, 3000)
	if err != nil || cleared.Purpose != nil || cleared.PurposeAssignedAt != nil {
		t.Errorf("clearing answered %+v (%v)", cleared, err)
	}
	lists, err := database.Blocklists(ctx)
	if err != nil || len(lists) != 1 || lists[0].Name != "an observed list name" {
		t.Errorf("the listing holds %+v (%v)", lists, err)
	}
}
