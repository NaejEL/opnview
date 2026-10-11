package broker_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// TestAMirrorReadDuringARefreshSeesOneConsistentDatum reproduces, under -race, the data race
// between a reader served from the mirror (fromMirror reading the datum's AttemptedAt, FetchedAt,
// Generation...) and a refresh storing a new generation of the same datum (mirror.Store writing
// them). Readers are now handed a copy of the datum's state taken under the datum's lock, and the
// mirror writes that state under the same lock. Without the fix, `go test -race` reports the race
// here; with it, every answer is either the mirror's or the firewall's, and never a mixture.
func TestAMirrorReadDuringARefreshSeesOneConsistentDatum(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodGet, "/api/example/racing/search", opnsense.NoBody)
	w.fake.answer(jsonAnswer(rowsBody))
	w.call("example_reader_0", ep, opnsense.RequestOptions{}, time.Hour, "a")

	const rounds = 20
	var group sync.WaitGroup
	failures := make(chan string, 4*rounds)
	group.Add(1)
	go func() {
		// The refresher: every call goes to the firewall and stores a new generation.
		defer group.Done()
		requester := broker.Requester{Name: "example_reader_0", Version: "1"}
		for round := 0; round < rounds; round++ {
			served, err := w.broker.Call(context.Background(), broker.Request{Requester: requester, Endpoint: ep,
				MaxAge: time.Hour, Fresh: true})
			if err != nil || served.ServedFrom != broker.ServedFromFirewall {
				failures <- "a fresh call was not answered by the firewall"
			}
		}
	}()
	for reader := 0; reader < 3; reader++ {
		group.Add(1)
		go func() {
			// The readers: every call is young enough to be served from the mirror.
			defer group.Done()
			requester := broker.Requester{Name: "example_reader_0", Version: "1"}
			for round := 0; round < rounds; round++ {
				served, err := w.broker.Call(context.Background(), broker.Request{Requester: requester,
					Endpoint: ep, MaxAge: time.Hour})
				if err != nil {
					failures <- err.Error()
					continue
				}
				if served.ServedFrom != broker.ServedFromMirror && served.ServedFrom != broker.ServedFromFirewall {
					failures <- "an answer was served from " + string(served.ServedFrom)
				}
				if !served.Response.OK() {
					failures <- "an answer from the mirror was not OK during a refresh"
				}
			}
		}()
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}
