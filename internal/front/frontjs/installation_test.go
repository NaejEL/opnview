package frontjs_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Collection, Interfaces, People and Blocklists views perform every action their JSON routes
// offer (F7-AC14), driven through the front against a fake API that records the requests.

// collectionAnswer is GET /api/collection: one sized kind, one polled kind, two setting cards.
func collectionAnswer(retention int) map[string]any {
	none := "rate_not_measured"
	return map[string]any{
		"kinds": []any{map[string]any{
			"kind": "firewall_log", "page_size_adjustable": true,
			"source_state": map[string]any{"availability": "healthy", "selection": "auto"},
			"in_force":     map[string]any{"page_size": 500, "interval_seconds": 30, "page_ceiling": nil, "page_state": nil},
			"measurement": map[string]any{"window_start_at": 1773392400, "window_end_at": 1773478800,
				"retention_seconds": retention, "covered_from_at": 1773400000, "covered_to_at": 1773478000,
				"record_count": 120000, "mean_rate": map[string]any{"per_second": 1.4, "state": nil},
				"ingested_mean_rate": map[string]any{"per_second": nil, "state": none},
				"peak_rate":          map[string]any{"per_second": 9.5, "state": nil}, "gap_count": 1, "missed_seconds": 600,
				"gaps": []any{map[string]any{"reason": "digest_outside_returned_window", "count": 1, "missed_seconds": 600}}},
			"suggestion":      map[string]any{"state": "suggestion_available", "page_size": 800, "interval_seconds": 20},
			"implementations": []any{map[string]any{"provider_key": "pf", "selection": "auto"}},
		}},
		"polled_kinds": []any{map[string]any{"kind": "resolver_cache", "interval_seconds": 300,
			"implementations": []any{map[string]any{"provider_key": "unbound", "selection": "auto", "availability": "healthy"}}}},
		"settings": []any{
			map[string]any{"card": "pairing", "settings": []any{map[string]any{"key": "leg_pairing_window_seconds", "value": 5}}},
			map[string]any{"card": "storage", "settings": []any{
				map[string]any{"key": "retention_seconds", "value": retention},
				map[string]any{"key": "aggregate_mode", "value": "full"}}},
		},
	}
}

func TestTheCollectionViewSavesAnIntervalAndConfirmsAShorterRetention(t *testing.T) {
	t.Parallel()
	server := newFakeServer(t, true, true)
	retention := 7776000
	server.on("GET /api/collection", func(frontjsRequest) frontjsAnswer { return ok(collectionAnswer(retention)) })
	server.on("POST /api/collection/kinds/firewall_log", func(frontjsRequest) frontjsAnswer {
		return ok(collectionAnswer(retention))
	})
	server.on("POST /api/collection/settings/storage", func(request frontjsRequest) frontjsAnswer {
		var body map[string]any
		_ = json.Unmarshal([]byte(request.Body), &body)
		requested := int(body["retention_seconds"].(float64))
		if requested < retention && body["confirm"] != float64(requested) {
			return frontjsAnswer{Status: 409, Body: map[string]any{"condition": "retention_confirmation_required",
				"in_force_retention_seconds": retention, "requested_retention_seconds": requested, "changes": []any{}}}
		}
		retention = requested
		return ok(collectionAnswer(retention))
	})
	page := server.page(t, "/collection")
	page.Input("#interval-firewall_log", "45")
	page.Click(`#card-kind-firewall_log button[type="submit"]`)
	if body := lastWrite(t, page.Writes(), "/api/collection/kinds/firewall_log"); body["interval_seconds"] != float64(45) ||
		body["page_size"] != float64(500) {
		t.Errorf("the interval save sent %v", body)
	}

	page.Input("#setting-retention_seconds", "86400")
	page.Click(`#card-settings-storage button[type="submit"]`)
	if page.Count("#card-settings-storage .confirm-panel") != 1 {
		t.Fatalf("shortening the retention does not ask to confirm:\n%s", page.Outline())
	}
	if retention != 7776000 {
		t.Fatal("the retention was shortened before the reader confirmed")
	}
	first := lastWrite(t, page.Writes(), "/api/collection/settings/storage")
	if _, sent := first["confirm"]; sent {
		t.Errorf("the first save carried a confirmation: %v", first)
	}
	page.Click(`#card-settings-storage .confirm-panel button.btn-destructive`)
	if body := lastWrite(t, page.Writes(), "/api/collection/settings/storage"); body["confirm"] != float64(86400) {
		t.Errorf("the confirmed save sent %v", body)
	}
	if retention != 86400 || page.Count("#card-settings-storage .confirm-panel") != 0 {
		t.Errorf("the confirmed save did not apply: retention %d", retention)
	}
}

// interfacesAnswer is GET /api/interface-networks.
func interfacesAnswer(label any, removed bool, linkLocal bool) map[string]any {
	return map[string]any{"interfaces": []any{
		map[string]any{"id": 3, "description": "Example LAN", "identifier": "opt1", "device": "igc1", "upstream": false,
			"link_local": linkLocal, "user_label": label, "overlaps": []any{},
			"networks": []any{
				map[string]any{"network": "192.0.2.0/24", "origin": "detected", "removed": removed, "counts": !removed,
					"not_counting_reason": nil},
				map[string]any{"network": "198.51.100.0/25", "origin": "operator", "removed": false, "counts": true,
					"not_counting_reason": nil}}},
		map[string]any{"id": 1, "description": "Example WAN", "identifier": "wan", "device": "igc0", "upstream": true,
			"link_local": false, "user_label": nil, "overlaps": []any{}, "networks": []any{}},
	}}
}

func TestTheInterfacesViewEditsNetworksTheLinkLocalRuleAndTheLabel(t *testing.T) {
	t.Parallel()
	server := newFakeServer(t, true, true)
	var label any
	removed, linkLocal := false, false
	server.on("GET /api/interface-networks", func(frontjsRequest) frontjsAnswer {
		return ok(interfacesAnswer(label, removed, linkLocal))
	})
	server.on("POST /api/interface-networks", func(request frontjsRequest) frontjsAnswer {
		var body map[string]any
		_ = json.Unmarshal([]byte(request.Body), &body)
		switch body["action"] {
		case "remove":
			removed = true
		case "restore":
			removed = false
		case "link_local":
			linkLocal = body["link_local"] == true
		}
		return ok(interfacesAnswer(label, removed, linkLocal))
	})
	server.on("POST /api/interfaces/3/label", func(request frontjsRequest) frontjsAnswer {
		var body map[string]any
		_ = json.Unmarshal([]byte(request.Body), &body)
		label = body["user_label"]
		return ok(map[string]any{"interface": map[string]any{"id": 3, "identifier": "opt1", "description": "Example LAN",
			"user_label": label}})
	})
	page := server.page(t, "/interfaces")
	// A label is never derived from a description: the field of an unlabelled interface is empty.
	if value := page.Run(`document.getElementById("label-3").value`).String(); value != "" {
		t.Errorf("the label field of an unlabelled interface holds %q", value)
	}
	writes := func() []map[string]any {
		var bodies []map[string]any
		for _, write := range page.Writes() {
			if write.Path == "/api/interface-networks" {
				var body map[string]any
				_ = json.Unmarshal([]byte(write.Body), &body)
				bodies = append(bodies, body)
			}
		}
		return bodies
	}
	page.Click(`#card-interface-3 tbody tr button`)
	page.Click(`#card-interface-3 tbody tr button.btn-destructive`)
	page.Click(`#card-interface-3 tbody tr button`)
	page.Choose("#link-local-3", "true")
	page.Input("#label-3", "Workshop")
	page.Click(`#card-interface-3 form button[type="submit"]`)

	var actions []string
	for _, body := range writes() {
		actions = append(actions, body["action"].(string))
		if body["interface"] != float64(3) {
			t.Errorf("an edit named the interface %v", body["interface"])
		}
	}
	got := strings.Join(actions, " ")
	for _, want := range []string{"confirm", "remove", "restore", "link_local"} {
		if !strings.Contains(got, want) {
			t.Errorf("the view did not send %s; it sent %s", want, got)
		}
	}
	if body := lastWrite(t, page.Writes(), "/api/interfaces/3/label"); body["user_label"] != "Workshop" {
		t.Errorf("the label save sent %v", body)
	}
	if linkLocal != true {
		t.Error("the link-local rule was not set")
	}
}

// addNetwork checks the add form separately, because its selector names the add form.
func TestTheInterfacesViewAddsANetwork(t *testing.T) {
	t.Parallel()
	server := newFakeServer(t, true, true)
	server.on("GET /api/interface-networks", func(frontjsRequest) frontjsAnswer { return ok(interfacesAnswer(nil, false, false)) })
	server.on("POST /api/interface-networks", func(frontjsRequest) frontjsAnswer { return ok(interfacesAnswer(nil, false, false)) })
	page := server.page(t, "/interfaces")
	page.Input("#network-3", "203.0.113.0/26")
	page.Run(`(function () {
  var field = document.getElementById("network-3");
  var form = field.parentNode;
  while (form.tagName !== "FORM") { form = form.parentNode; }
  form.dispatchEvent(__makeEvent("submit"));
})();`)
	body := lastWrite(t, page.Writes(), "/api/interface-networks")
	if body["action"] != "add" || body["network"] != "203.0.113.0/26" {
		t.Errorf("the add sent %v", body)
	}
}

// peopleFixture holds the people and the machines of the People test.
type peopleFixture struct {
	owners  []map[string]any
	clients []map[string]any
}

func (f *peopleFixture) owner(id float64) map[string]any {
	for _, owner := range f.owners {
		if owner["id"] == id {
			return owner
		}
	}
	return nil
}

func TestThePeopleViewCreatesRenamesAssignsUnassignsAndDeletesThenUndoes(t *testing.T) {
	t.Parallel()
	server := newFakeServer(t, true, true)
	fixture := &peopleFixture{clients: []map[string]any{{
		"id": float64(10), "identity_kind": "mac", "identity_key": "02:00:5e:10:00:01", "hostname": "laptop.example",
		"mac": "02:00:5e:10:00:01", "last_address": "192.0.2.10", "unstable_identity": false, "owner": nil,
		"owner_assigned_at": nil, "first_seen_at": 1773400000, "last_seen_at": 1773478000,
		"interface": map[string]any{"id": 3, "identifier": "opt1", "description": "Example LAN", "user_label": nil}}}}
	server.on("GET /api/owners", func(frontjsRequest) frontjsAnswer {
		owners := []any{}
		for _, owner := range fixture.owners {
			owners = append(owners, owner)
		}
		return ok(map[string]any{"owners": owners})
	})
	server.on("GET /api/clients", func(frontjsRequest) frontjsAnswer {
		clients := []any{}
		for _, client := range fixture.clients {
			clients = append(clients, client)
		}
		return ok(map[string]any{"clients": clients})
	})
	server.on("POST /api/owners", func(request frontjsRequest) frontjsAnswer {
		var body map[string]any
		_ = json.Unmarshal([]byte(request.Body), &body)
		owner := map[string]any{"id": float64(1), "display_name": body["display_name"], "client_count": 0}
		fixture.owners = append(fixture.owners, owner)
		return ok(map[string]any{"owner": owner})
	})
	server.on("POST /api/owners/1/rename", func(request frontjsRequest) frontjsAnswer {
		var body map[string]any
		_ = json.Unmarshal([]byte(request.Body), &body)
		fixture.owner(1)["display_name"] = body["display_name"]
		return ok(map[string]any{"owner": fixture.owner(1)})
	})
	server.on("POST /api/owners/1/assign", func(frontjsRequest) frontjsAnswer {
		fixture.clients[0]["owner"] = map[string]any{"id": 1, "display_name": fixture.owner(1)["display_name"]}
		fixture.clients[0]["owner_assigned_at"] = 1773478800
		return ok(map[string]any{"assignments": []any{}})
	})
	server.on("POST /api/clients/unassign", func(frontjsRequest) frontjsAnswer {
		fixture.clients[0]["owner"] = nil
		fixture.clients[0]["owner_assigned_at"] = nil
		return ok(map[string]any{"assignments": []any{}})
	})
	server.on("POST /api/owners/1/delete", func(frontjsRequest) frontjsAnswer {
		name := fixture.owner(1)["display_name"]
		fixture.owners = nil
		fixture.clients[0]["owner"] = nil
		fixture.clients[0]["owner_assigned_at"] = nil
		return ok(map[string]any{"id": 1, "display_name": name,
			"cleared": []any{map[string]any{"client": 10, "owner_assigned_at": 1773478800}}})
	})
	var restored map[string]any
	server.on("POST /api/owners/restore", func(request frontjsRequest) frontjsAnswer {
		_ = json.Unmarshal([]byte(request.Body), &restored)
		owner := map[string]any{"id": float64(1), "display_name": restored["display_name"], "client_count": 0}
		fixture.owners = []map[string]any{owner}
		return ok(map[string]any{"owner": owner, "restored": []any{},
			"not_restored": []any{map[string]any{"client": 10, "reason": "assigned_elsewhere", "owner_id": 2}}})
	})
	page := server.page(t, "/people")
	page.Input("#person-name", "Noé Example")
	page.Click(`form.people-add button[type="submit"]`)
	if body := lastWrite(t, page.Writes(), "/api/owners"); body["display_name"] != "Noé Example" {
		t.Fatalf("the creation sent %v", body)
	}
	page.Click(`.person button`)
	page.Input("#rename-1", "Noé Renamed")
	page.Click(`.person button[type="submit"]`)
	if body := lastWrite(t, page.Writes(), "/api/owners/1/rename"); body["display_name"] != "Noé Renamed" {
		t.Fatalf("the rename sent %v", body)
	}
	page.Choose("#owner-10", "1")
	if body := lastWrite(t, page.Writes(), "/api/owners/1/assign"); !strings.Contains(asJSON(body["clients"]), "10") {
		t.Fatalf("the assignment sent %v", body)
	}
	page.Choose("#owner-10", "")
	if body := lastWrite(t, page.Writes(), "/api/clients/unassign"); !strings.Contains(asJSON(body["clients"]), "10") {
		t.Fatalf("the unassignment sent %v", body)
	}
	page.Choose("#owner-10", "1")
	page.Click(`.person button.btn-destructive`)
	if page.Count(".undo-panel button") != 1 {
		t.Fatalf("the deletion offers no undo:\n%s", page.Outline())
	}
	page.Click(".undo-panel button")
	if restored == nil || restored["display_name"] != "Noé Renamed" || !strings.Contains(asJSON(restored["cleared"]), "1773478800") {
		t.Fatalf("the undo did not send the deletion's answer unchanged: %v", restored)
	}
	if page.Count(".not-restored li") != 1 || !strings.Contains(page.Text(".not-restored"),
		englishCatalogue(t)["people.not_restored.assigned_elsewhere"]) {
		t.Errorf("the view does not report what was not restored:\n%s", page.Outline())
	}
}

func asJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestTheBlocklistsViewAssignsAndClearsAPurpose(t *testing.T) {
	t.Parallel()
	server := newFakeServer(t, true, true)
	var purpose any
	server.on("GET /api/blocklists", func(frontjsRequest) frontjsAnswer {
		return ok(map[string]any{"blocklists": []any{map[string]any{"id": 4, "name": "ads.example", "purpose": purpose,
			"purpose_assigned_at": nil, "first_seen_at": 1773400000, "last_seen_at": 1773478000}},
			"purposes": []string{"advertising", "tracking", "threat", "parental", "other"}})
	})
	server.on("POST /api/blocklists/4/purpose", func(request frontjsRequest) frontjsAnswer {
		var body map[string]any
		_ = json.Unmarshal([]byte(request.Body), &body)
		purpose = body["purpose"]
		return ok(map[string]any{"blocklist": map[string]any{"id": 4}})
	})
	page := server.page(t, "/blocklists")
	page.Choose("#purpose-4", "tracking")
	if body := lastWrite(t, page.Writes(), "/api/blocklists/4/purpose"); body["purpose"] != "tracking" {
		t.Fatalf("the assignment sent %v", body)
	}
	page.Choose("#purpose-4", "")
	if body := lastWrite(t, page.Writes(), "/api/blocklists/4/purpose"); body["purpose"] != nil {
		t.Fatalf("clearing sent %v", body)
	}
}
