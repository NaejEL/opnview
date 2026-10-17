package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// TestTheClientListingPagesByKeyWithoutSkippingOrRepeating is S5A-AC35's cursor clause (PA18.1 of
// specs/SPEC-backend-completion-plugins.md): the listing is read page by page after the last id of the
// previous page while other clients are inserted concurrently, and every client that existed when the
// listing began is read exactly once, in id order; the first page is the first clients by id.
func TestTheClientListingPagesByKeyWithoutSkippingOrRepeating(t *testing.T) {
	t.Parallel()
	database, _ := openTestStore(t)
	ctx := context.Background()
	newClient := func(index int) int64 {
		t.Helper()
		mac := fmt.Sprintf("00:00:5e:00:53:%02x", index%256)
		id, err := database.UpsertClient(ctx, vocab.Client{Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC,
			Key: fmt.Sprintf("%s-%d", mac, index)}, MAC: &mac}, 1750000000)
		if err != nil {
			t.Error(err)
		}
		return id
	}
	existing := map[int64]bool{}
	for index := 0; index < 47; index++ {
		existing[newClient(index)] = true
	}
	first, err := database.ClientsPage(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	every, err := database.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index, client := range first {
		if every[index].ID != client.ID {
			t.Fatalf("the first page is not the first clients by id: %d at %d, want %d", client.ID, index,
				every[index].ID)
		}
	}
	var inserting sync.WaitGroup
	inserting.Add(1)
	go func() {
		defer inserting.Done()
		for index := 1000; index < 1060; index++ {
			newClient(index)
		}
	}()
	seen := map[int64]int{}
	after, previous := int64(0), int64(0)
	for {
		page, err := database.ClientsPage(ctx, after, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, client := range page {
			seen[client.ID]++
			if client.ID <= previous {
				t.Errorf("the client %d follows %d", client.ID, previous)
			}
			previous = client.ID
		}
		if len(page) < 10 {
			break
		}
		after = page[len(page)-1].ID
	}
	inserting.Wait()
	for id := range existing {
		if seen[id] != 1 {
			t.Errorf("the client %d, present when the listing began, was read %d times", id, seen[id])
		}
	}
	for id, times := range seen {
		if times != 1 {
			t.Errorf("the client %d was read %d times", id, times)
		}
	}
}
