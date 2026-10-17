package owners

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// People, the machines assigned to them, interface labels and blocklist purposes: the user input
// of C1 (specs/SPEC-backend-completion-plugins.md). Every one of them is assigned, never inferred:
// each write below names its rows by id and changes exactly those, and nothing here matches on a
// hostname, a MAC, a vendor hint, an address or a name (docs/data-model.md, "client", "owner",
// "blocklist"). Nothing here contacts the firewall.
//
// A CHANGE OF OWNERSHIP INVALIDATES THE OWNER FAMILY. Ownership is an attribute of the person, not
// of the flow, so the refresh contract's recognition rule cannot see it (docs/data-model.md,
// "Refresh contract", rule 3). Every write that moves a machine between people records, in its own
// transaction, the kept hour slots holding the machine's traffic in owner_reassigned_hour; the next
// refresh rewrites them as forced instants (RefreshAggregatesAt) and the widget API reports them
// stale until it has.

// The closed refusal codes of the writes below. An answer carries the code, never a sentence.
const (
	// RefusalDisplayNameEmpty is a display name that is empty once trimmed.
	RefusalDisplayNameEmpty = "display_name_empty"
	// RefusalDisplayNameTaken is a display name another person already holds, compared as the
	// schema's UNIQUE compares it: byte for byte, after trimming.
	RefusalDisplayNameTaken = "display_name_taken"
	// RefusalUnknownOwner is a person id naming nobody.
	RefusalUnknownOwner = "unknown_owner"
	// RefusalUnknownClient is a client id naming no machine.
	RefusalUnknownClient = "unknown_client"
	// RefusalNoClients is an assignment naming no client.
	RefusalNoClients = "no_clients"
	// RefusalDuplicateClient is an assignment naming one client twice.
	RefusalDuplicateClient = "duplicate_client"
	// RefusalInvalidInstant is an owner_assigned_at to restore outside the schema's range.
	RefusalInvalidInstant = "invalid_owner_assigned_at"
	// RefusalUnknownInterface is an interface id naming no interface.
	RefusalUnknownInterface = "unknown_interface"
	// RefusalLabelEmpty is a label that is empty once trimmed; clearing a label is a null.
	RefusalLabelEmpty = "user_label_empty"
	// RefusalUnknownBlocklist is a blocklist id naming no list.
	RefusalUnknownBlocklist = "unknown_blocklist"
	// RefusalPurposeNotInVocabulary is a purpose outside the schema's CHECK vocabulary.
	RefusalPurposeNotInVocabulary = "purpose_not_in_vocabulary"
)

// The reasons an undone deletion leaves one assignment as it is.
const (
	// NotRestoredAssignedElsewhere is a client assigned to someone else since the deletion.
	NotRestoredAssignedElsewhere = "assigned_elsewhere"
	// NotRestoredClientGone is a client that no longer exists.
	NotRestoredClientGone = "client_not_found"
)

// BlocklistPurposes is the vocabulary of blocklist.purpose, exactly the schema's CHECK
// (docs/data-model.md, Vocabulary). A test reads the constraint back and compares.
var BlocklistPurposes = []string{"advertising", "tracking", "threat", "parental", "other"}

// maxInstant is the schema's upper bound on every instant column.
const maxInstant = 4102444800

// UserInputRefusal is a write refused for a closed reason; nothing was written.
type UserInputRefusal struct {
	// Code is one of the Refusal* constants.
	Code string
	// ClientID names the client when one client is the reason.
	ClientID int64
}

func (r *UserInputRefusal) Error() string {
	if r.ClientID != 0 {
		return fmt.Sprintf("store: refused (%s, client %d)", r.Code, r.ClientID)
	}
	return "store: refused (" + r.Code + ")"
}

func refuse(code string) error { return &UserInputRefusal{Code: code} }

// Owner is one person.
type Owner struct {
	ID          int64
	DisplayName string
	CreatedAt   int64
	UpdatedAt   int64
	// ClientCount is the number of machines assigned to the person.
	ClientCount int64
}

// Assignment is one machine's assignment: the client and when it was assigned.
type Assignment struct {
	ClientID        int64
	OwnerAssignedAt int64
}

// DeletedOwner is what a deletion removed: the person and every assignment it cleared, enough to
// undo it.
type DeletedOwner struct {
	ID          int64
	DisplayName string
	Cleared     []Assignment
}

// NotRestored is one assignment an undone deletion left as it is, and why.
type NotRestored struct {
	ClientID int64
	// Reason is NotRestoredAssignedElsewhere or NotRestoredClientGone.
	Reason string
	// OwnerID is the person the client is assigned to now, for NotRestoredAssignedElsewhere.
	OwnerID *int64
}

// RestoredOwner is an undone deletion: the person created again, the assignments restored with
// their original instants, and those left as they are.
type RestoredOwner struct {
	Owner       Owner
	Restored    []Assignment
	NotRestored []NotRestored
}

// AssignmentChange is one client an assignment or an unassignment named, as it now stands.
type AssignmentChange struct {
	ClientID        int64
	OwnerID         *int64
	OwnerAssignedAt *int64
	// Changed is false when the client already stood as asked: assigned to that person (its
	// original instant kept), or already unassigned.
	Changed bool
}

// ListedClient is one machine of the client listing.
type ListedClient struct {
	ID                   int64
	IdentityKind         string
	IdentityKey          string
	Hostname             *string
	MAC                  *string
	LastAddress          *string
	InterfaceID          *int64
	InterfaceIdentifier  *string
	InterfaceDescription *string
	InterfaceUserLabel   *string
	UnstableIdentity     bool
	OwnerID              *int64
	OwnerDisplayName     *string
	OwnerAssignedAt      *int64
	FirstSeenAt          int64
	LastSeenAt           int64
}

// LabelledInterface is an interface as a label write leaves it.
type LabelledInterface struct {
	ID          int64
	Identifier  string
	Description string
	UserLabel   *string
}

// Blocklist is one list the resolver refused a lookup against, with its purpose.
type Blocklist struct {
	ID                int64
	Name              string
	Purpose           *string
	PurposeAssignedAt *int64
	FirstSeenAt       int64
	LastSeenAt        int64
}

// Owners lists every person, by display name, each with its machine count.
func (s *OwnerStore) Owners(ctx context.Context) ([]Owner, error) {
	text, err := sqldb.Statement("owners_list")
	if err != nil {
		return nil, err
	}
	rows, err := s.SQL.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("store: owners_list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	owners := []Owner{}
	for rows.Next() {
		var owner Owner
		if err := rows.Scan(&owner.ID, &owner.DisplayName, &owner.CreatedAt, &owner.UpdatedAt,
			&owner.ClientCount); err != nil {
			return nil, fmt.Errorf("store: owners_list: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: owners_list: %w", err)
	}
	return owners, nil
}

// displayName trims a display name and refuses an empty one.
func displayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", refuse(RefusalDisplayNameEmpty)
	}
	return name, nil
}

// nameTaken reports whether another person than id holds the name.
func nameTaken(ctx context.Context, q sqldb.Querier, name string, id int64) (bool, error) {
	taken, _, err := sqldb.QueryOptionalInt(ctx, q, "owner_name_taken", map[string]any{"name": name, "id": id})
	return taken > 0, err
}

// createOwner inserts a person in a transaction, refusing an empty or taken name.
func createOwner(ctx context.Context, tx *sql.Tx, raw string, now int64) (Owner, error) {
	name, err := displayName(raw)
	if err != nil {
		return Owner{}, err
	}
	taken, err := nameTaken(ctx, tx, name, 0)
	if err != nil {
		return Owner{}, err
	}
	if taken {
		return Owner{}, refuse(RefusalDisplayNameTaken)
	}
	id, _, err := sqldb.QueryOptionalInt(ctx, tx, "owner_insert", map[string]any{"name": name, "now": now})
	if err != nil {
		if sqldb.IsUniqueViolation(err) {
			return Owner{}, refuse(RefusalDisplayNameTaken)
		}
		return Owner{}, err
	}
	return Owner{ID: id, DisplayName: name, CreatedAt: now, UpdatedAt: now}, nil
}

// CreateOwner creates a person. The display name is trimmed; an empty or taken one is refused with
// a *UserInputRefusal and nothing is written.
func (s *OwnerStore) CreateOwner(ctx context.Context, name string, now int64) (Owner, error) {
	var created Owner
	err := s.inTransaction(ctx, "creating a person", func(tx *sql.Tx) error {
		var err error
		created, err = createOwner(ctx, tx, name, now)
		return err
	})
	return created, err
}

// lockOwner reads one person, locked; an unknown id is refused.
func lockOwner(ctx context.Context, tx *sql.Tx, id int64) (Owner, error) {
	text, err := sqldb.Statement("owner_lock")
	if err != nil {
		return Owner{}, err
	}
	var owner Owner
	err = sqldb.QueryRow(ctx, tx, text, map[string]any{"id": id}).Scan(&owner.ID,
		&owner.DisplayName, &owner.CreatedAt, &owner.UpdatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Owner{}, refuse(RefusalUnknownOwner)
	case err != nil:
		return Owner{}, fmt.Errorf("store: owner_lock: %w", err)
	}
	return owner, nil
}

// RenameOwner renames a person under the same rules as CreateOwner. Renaming a person to the name
// they already hold changes nothing and is not refused.
func (s *OwnerStore) RenameOwner(ctx context.Context, id int64, name string, now int64) (Owner, error) {
	var renamed Owner
	err := s.inTransaction(ctx, "renaming a person", func(tx *sql.Tx) error {
		owner, err := lockOwner(ctx, tx, id)
		if err != nil {
			return err
		}
		trimmed, err := displayName(name)
		if err != nil {
			return err
		}
		if trimmed == owner.DisplayName {
			renamed = owner
			return nil
		}
		taken, err := nameTaken(ctx, tx, trimmed, id)
		if err != nil {
			return err
		}
		if taken {
			return refuse(RefusalDisplayNameTaken)
		}
		if _, err := sqldb.ExecNamed(ctx, tx, "owner_rename", map[string]any{"id": id, "name": trimmed, "now": now}); err != nil {
			if sqldb.IsUniqueViolation(err) {
				return refuse(RefusalDisplayNameTaken)
			}
			return err
		}
		owner.DisplayName, owner.UpdatedAt = trimmed, now
		renamed = owner
		return nil
	})
	if err != nil {
		return Owner{}, err
	}
	renamed.ClientCount, err = s.ownerClientCount(ctx, renamed.ID)
	return renamed, err
}

// ownerClientCount is one person's machine count.
func (s *OwnerStore) ownerClientCount(ctx context.Context, id int64) (int64, error) {
	owners, err := s.Owners(ctx)
	if err != nil {
		return 0, err
	}
	for _, owner := range owners {
		if owner.ID == id {
			return owner.ClientCount, nil
		}
	}
	return 0, nil
}

// DeleteOwner deletes a person: every assignment is cleared and returned, with its instant, so the
// deletion can be undone (RestoreOwner). The owner family's rows of the person go with them, and
// every slot they stood in is recorded for the next refresh, which rewrites it with the machines in
// the unassigned bucket.
func (s *OwnerStore) DeleteOwner(ctx context.Context, id int64, now int64) (DeletedOwner, error) {
	var deleted DeletedOwner
	err := s.inTransaction(ctx, "deleting a person", func(tx *sql.Tx) error {
		owner, err := lockOwner(ctx, tx, id)
		if err != nil {
			return err
		}
		cleared, err := ownerAssignments(ctx, tx, id)
		if err != nil {
			return err
		}
		parameters := map[string]any{"id": id, "now": now}
		for _, name := range []string{"owner_record_slots", "owner_delete_slots_1h", "owner_delete_slots_24h",
			"owner_delete_slots_7d", "owner_delete_slots_30d", "owner_clear_assignments", "owner_delete"} {
			if _, err := sqldb.ExecNamed(ctx, tx, name, parameters); err != nil {
				return err
			}
		}
		deleted = DeletedOwner{ID: owner.ID, DisplayName: owner.DisplayName, Cleared: cleared}
		return nil
	})
	return deleted, err
}

// ownerAssignments reads one person's machines, locked.
func ownerAssignments(ctx context.Context, tx *sql.Tx, id int64) ([]Assignment, error) {
	text, err := sqldb.Statement("owner_assignments")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, tx, text, map[string]any{"id": id})
	if err != nil {
		return nil, fmt.Errorf("store: owner_assignments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	assignments := []Assignment{}
	for rows.Next() {
		var assignment Assignment
		if err := rows.Scan(&assignment.ClientID, &assignment.OwnerAssignedAt); err != nil {
			return nil, fmt.Errorf("store: owner_assignments: %w", err)
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

// RestoreOwner undoes a deletion: it creates the person again under the name given and restores
// each assignment given with its ORIGINAL owner_assigned_at. A client assigned to someone else in
// the meantime, or gone, is reported in NotRestored and left as it is. A taken or empty name, or an
// instant outside the schema's range, is refused and nothing is written.
func (s *OwnerStore) RestoreOwner(ctx context.Context, name string, assignments []Assignment, now int64) (
	RestoredOwner, error) {
	var restored RestoredOwner
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(assignments))
	for _, assignment := range assignments {
		if assignment.OwnerAssignedAt < 0 || assignment.OwnerAssignedAt >= maxInstant {
			return restored, &UserInputRefusal{Code: RefusalInvalidInstant, ClientID: assignment.ClientID}
		}
		if seen[assignment.ClientID] {
			return restored, &UserInputRefusal{Code: RefusalDuplicateClient, ClientID: assignment.ClientID}
		}
		seen[assignment.ClientID] = true
		ids = append(ids, assignment.ClientID)
	}
	err := s.inTransaction(ctx, "restoring a person", func(tx *sql.Tx) error {
		owner, err := createOwner(ctx, tx, name, now)
		if err != nil {
			return err
		}
		current, err := lockClients(ctx, tx, ids)
		if err != nil {
			return err
		}
		restored = RestoredOwner{Owner: owner, Restored: []Assignment{}, NotRestored: []NotRestored{}}
		var moved []int64
		for _, assignment := range assignments {
			state, exists := current[assignment.ClientID]
			switch {
			case !exists:
				restored.NotRestored = append(restored.NotRestored,
					NotRestored{ClientID: assignment.ClientID, Reason: NotRestoredClientGone})
				continue
			case state.ownerID != nil:
				restored.NotRestored = append(restored.NotRestored, NotRestored{ClientID: assignment.ClientID,
					Reason: NotRestoredAssignedElsewhere, OwnerID: state.ownerID})
				continue
			}
			if _, err := sqldb.ExecNamed(ctx, tx, "client_assign", map[string]any{"id": assignment.ClientID,
				"owner_id": owner.ID, "at": assignment.OwnerAssignedAt}); err != nil {
				return err
			}
			moved = append(moved, assignment.ClientID)
			restored.Restored = append(restored.Restored, assignment)
		}
		restored.Owner.ClientCount = int64(len(moved))
		return recordClientHours(ctx, tx, moved, now)
	})
	if err != nil {
		return RestoredOwner{}, err
	}
	return restored, nil
}

// clientState is one locked client's ownership.
type clientState struct {
	ownerID         *int64
	ownerAssignedAt *int64
}

// lockClients reads the named clients' ownership, locked; a client that does not exist is absent.
func lockClients(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]clientState, error) {
	states := map[int64]clientState{}
	if len(ids) == 0 {
		return states, nil
	}
	text, err := sqldb.Statement("clients_lock")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, tx, text, map[string]any{"ids": jsonIDs(ids)})
	if err != nil {
		return nil, fmt.Errorf("store: clients_lock: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id         int64
			owner, at  sql.NullInt64
			stateOfRow clientState
		)
		if err := rows.Scan(&id, &owner, &at); err != nil {
			return nil, fmt.Errorf("store: clients_lock: %w", err)
		}
		if owner.Valid {
			ownerID, assignedAt := owner.Int64, at.Int64
			stateOfRow = clientState{ownerID: &ownerID, ownerAssignedAt: &assignedAt}
		}
		states[id] = stateOfRow
	}
	return states, rows.Err()
}

// recordClientHours records every kept hour slot holding the clients' traffic for the next
// refresh.
func recordClientHours(ctx context.Context, tx *sql.Tx, ids []int64, now int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := sqldb.ExecNamed(ctx, tx, "clients_record_hours", map[string]any{"ids": jsonIDs(ids), "now": now})
	return err
}

// jsonIDs encodes ids as the JSON array the statements read.
func jsonIDs(ids []int64) string {
	encoded, _ := json.Marshal(ids)
	return string(encoded)
}

// checkClientList refuses an empty list and a client named twice.
func checkClientList(ids []int64) error {
	if len(ids) == 0 {
		return refuse(RefusalNoClients)
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			return &UserInputRefusal{Code: RefusalDuplicateClient, ClientID: id}
		}
		seen[id] = true
	}
	return nil
}

// AssignClients assigns the named clients to one person, owner_assigned_at set to now. It changes
// exactly the clients it names: a client already assigned to that person keeps its original
// instant, and an unknown client refuses the whole write, which then changes nothing. A client with
// an unstable identity is assigned like any other.
func (s *OwnerStore) AssignClients(ctx context.Context, ownerID int64, clientIDs []int64, now int64) (
	[]AssignmentChange, error) {
	if err := checkClientList(clientIDs); err != nil {
		return nil, err
	}
	var changes []AssignmentChange
	err := s.inTransaction(ctx, "assigning clients", func(tx *sql.Tx) error {
		if _, err := lockOwner(ctx, tx, ownerID); err != nil {
			return err
		}
		current, err := lockClients(ctx, tx, clientIDs)
		if err != nil {
			return err
		}
		changes = make([]AssignmentChange, 0, len(clientIDs))
		var moved []int64
		for _, id := range clientIDs {
			state, exists := current[id]
			if !exists {
				return &UserInputRefusal{Code: RefusalUnknownClient, ClientID: id}
			}
			if state.ownerID != nil && *state.ownerID == ownerID {
				changes = append(changes, AssignmentChange{ClientID: id, OwnerID: state.ownerID,
					OwnerAssignedAt: state.ownerAssignedAt})
				continue
			}
			if _, err := sqldb.ExecNamed(ctx, tx, "client_assign", map[string]any{"id": id, "owner_id": ownerID,
				"at": now}); err != nil {
				return err
			}
			owner, at := ownerID, now
			changes = append(changes, AssignmentChange{ClientID: id, OwnerID: &owner, OwnerAssignedAt: &at,
				Changed: true})
			moved = append(moved, id)
		}
		return recordClientHours(ctx, tx, moved, now)
	})
	if err != nil {
		return nil, err
	}
	return changes, nil
}

// UnassignClients clears the named clients' owner. An unknown client refuses the whole write; a
// client already unassigned is reported unchanged.
func (s *OwnerStore) UnassignClients(ctx context.Context, clientIDs []int64, now int64) ([]AssignmentChange, error) {
	if err := checkClientList(clientIDs); err != nil {
		return nil, err
	}
	var changes []AssignmentChange
	err := s.inTransaction(ctx, "unassigning clients", func(tx *sql.Tx) error {
		current, err := lockClients(ctx, tx, clientIDs)
		if err != nil {
			return err
		}
		changes = make([]AssignmentChange, 0, len(clientIDs))
		var moved []int64
		for _, id := range clientIDs {
			state, exists := current[id]
			if !exists {
				return &UserInputRefusal{Code: RefusalUnknownClient, ClientID: id}
			}
			if state.ownerID == nil {
				changes = append(changes, AssignmentChange{ClientID: id})
				continue
			}
			if _, err := sqldb.ExecNamed(ctx, tx, "client_unassign", map[string]any{"id": id}); err != nil {
				return err
			}
			changes = append(changes, AssignmentChange{ClientID: id, Changed: true})
			moved = append(moved, id)
		}
		return recordClientHours(ctx, tx, moved, now)
	})
	if err != nil {
		return nil, err
	}
	return changes, nil
}

// Clients lists every machine, with or without traffic, by id.
func (s *OwnerStore) Clients(ctx context.Context) ([]ListedClient, error) {
	text, err := sqldb.Statement("clients_list")
	if err != nil {
		return nil, err
	}
	rows, err := s.SQL.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("store: clients_list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	clients := []ListedClient{}
	for rows.Next() {
		var (
			client                                  ListedClient
			hostname, mac, address                  sql.NullString
			identifier, description, label, ownerOf sql.NullString
			interfaceID, ownerID, assignedAt        sql.NullInt64
			unstable                                int64
		)
		if err := rows.Scan(&client.ID, &client.IdentityKind, &client.IdentityKey, &hostname, &mac, &address,
			&interfaceID, &identifier, &description, &label, &unstable, &ownerID, &ownerOf, &assignedAt,
			&client.FirstSeenAt, &client.LastSeenAt); err != nil {
			return nil, fmt.Errorf("store: clients_list: %w", err)
		}
		client.Hostname, client.MAC, client.LastAddress = nullText(hostname), nullText(mac), nullText(address)
		client.InterfaceID, client.InterfaceIdentifier = sqldb.NullInt(interfaceID), nullText(identifier)
		client.InterfaceDescription, client.InterfaceUserLabel = nullText(description), nullText(label)
		client.UnstableIdentity = unstable == 1
		client.OwnerID, client.OwnerDisplayName, client.OwnerAssignedAt = sqldb.NullInt(ownerID), nullText(ownerOf),
			sqldb.NullInt(assignedAt)
		clients = append(clients, client)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: clients_list: %w", err)
	}
	return clients, nil
}

func nullText(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

// SetInterfaceUserLabel sets an interface's label, trimmed, or clears it when label is nil. An
// empty label once trimmed and an unknown interface are refused. Discovery never writes the
// column, so the label survives every refresh of discovery.
func (s *OwnerStore) SetInterfaceUserLabel(ctx context.Context, id int64, label *string) (LabelledInterface, error) {
	var value any
	if label != nil {
		trimmed := strings.TrimSpace(*label)
		if trimmed == "" {
			return LabelledInterface{}, refuse(RefusalLabelEmpty)
		}
		value = trimmed
	}
	var labelled LabelledInterface
	err := s.inTransaction(ctx, "labelling an interface", func(tx *sql.Tx) error {
		changed, err := sqldb.ExecNamed(ctx, tx, "interface_set_user_label", map[string]any{"id": id, "label": value})
		if err != nil {
			return err
		}
		if changed == 0 {
			return refuse(RefusalUnknownInterface)
		}
		text, err := sqldb.Statement("interface_label_read")
		if err != nil {
			return err
		}
		var userLabel sql.NullString
		if err := sqldb.QueryRow(ctx, tx, text, map[string]any{"id": id}).Scan(&labelled.ID,
			&labelled.Identifier, &labelled.Description, &userLabel); err != nil {
			return fmt.Errorf("store: interface_label_read: %w", err)
		}
		labelled.UserLabel = nullText(userLabel)
		return nil
	})
	return labelled, err
}

// Blocklists lists every blocklist, by name.
func (s *OwnerStore) Blocklists(ctx context.Context) ([]Blocklist, error) {
	text, err := sqldb.Statement("blocklists_list")
	if err != nil {
		return nil, err
	}
	rows, err := s.SQL.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("store: blocklists_list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	lists := []Blocklist{}
	for rows.Next() {
		list, err := scanBlocklist(rows)
		if err != nil {
			return nil, err
		}
		lists = append(lists, list)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: blocklists_list: %w", err)
	}
	return lists, nil
}

// scanBlocklist reads one blocklist row.
func scanBlocklist(row interface{ Scan(...any) error }) (Blocklist, error) {
	var (
		list    Blocklist
		purpose sql.NullString
		at      sql.NullInt64
	)
	if err := row.Scan(&list.ID, &list.Name, &purpose, &at, &list.FirstSeenAt, &list.LastSeenAt); err != nil {
		return Blocklist{}, fmt.Errorf("store: reading a blocklist: %w", err)
	}
	list.Purpose, list.PurposeAssignedAt = nullText(purpose), sqldb.NullInt(at)
	return list, nil
}

// SetBlocklistPurpose sets a list's purpose within the schema's vocabulary, purpose_assigned_at set
// to now, or clears both when purpose is nil. A purpose outside the vocabulary and an unknown list
// are refused.
func (s *OwnerStore) SetBlocklistPurpose(ctx context.Context, id int64, purpose *string, now int64) (Blocklist, error) {
	var value, at any
	if purpose != nil {
		known := false
		for _, candidate := range BlocklistPurposes {
			known = known || candidate == *purpose
		}
		if !known {
			return Blocklist{}, refuse(RefusalPurposeNotInVocabulary)
		}
		value, at = *purpose, now
	}
	var list Blocklist
	err := s.inTransaction(ctx, "assigning a purpose", func(tx *sql.Tx) error {
		changed, err := sqldb.ExecNamed(ctx, tx, "blocklist_set_purpose", map[string]any{"id": id, "purpose": value,
			"at": at})
		if err != nil {
			return err
		}
		if changed == 0 {
			return refuse(RefusalUnknownBlocklist)
		}
		text, err := sqldb.Statement("blocklist_read")
		if err != nil {
			return err
		}
		list, err = scanBlocklist(sqldb.QueryRow(ctx, tx, text, map[string]any{"id": id}))
		return err
	})
	return list, err
}

// reassignedHours are the rows of owner_reassigned_hour one refresh read.
type reassignedHours struct {
	IDs   []int64
	Hours []int64
}

// ReadReassignedHours reads every hour a change of ownership recorded.
func ReadReassignedHours(ctx context.Context, q sqldb.Querier) (reassignedHours, error) {
	var read reassignedHours
	text, err := sqldb.Statement("reassigned_hours")
	if err != nil {
		return read, err
	}
	rows, err := q.QueryContext(ctx, text)
	if err != nil {
		return read, fmt.Errorf("store: reassigned_hours: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, hour int64
		if err := rows.Scan(&id, &hour); err != nil {
			return read, fmt.Errorf("store: reassigned_hours: %w", err)
		}
		read.IDs = append(read.IDs, id)
		read.Hours = append(read.Hours, hour)
	}
	return read, rows.Err()
}

// ForgetReassignedHours deletes exactly the rows a refresh read, once it has rewritten their slots;
// a row a change recorded while the refresh ran is kept for the next one.
func ForgetReassignedHours(ctx context.Context, q sqldb.Querier, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := sqldb.ExecNamed(ctx, q, "reassigned_hours_forget", map[string]any{"ids": jsonIDs(ids)})
	return err
}

// inTransaction runs work in one transaction, committed when work returns nil.
func (s *OwnerStore) inTransaction(ctx context.Context, what string, work func(tx *sql.Tx) error) error {
	tx, err := s.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning %s: %w", what, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := work(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing %s: %w", what, err)
	}
	return nil
}
