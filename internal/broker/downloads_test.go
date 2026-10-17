package broker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin/fetch"
)

// TestADownloadStaysOutOfTheQueue is BR-AC24: with the broker saturated -- its one call slot held
// by a firewall call that does not answer -- a fetch through the fetch facility completes on its
// own timing, and appears neither in the broker's counters nor in the mirror.
func TestADownloadStaysOutOfTheQueue(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.broker.Scheduler().SetSettings(schedule.Settings{MaxInFlight: 1, MaxPerSecond: 1})
	release := make(chan struct{})
	held := make(chan struct{}, 1)
	w.fake.AnswerWith(func(*http.Request, string) (int, string, string) {
		held <- struct{}{}
		<-release
		return http.StatusOK, "application/json", rowsBody
	})
	stuck := endpoint(t, http.MethodGet, "/api/example/stuck/get", opnsense.NoBody)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = w.broker.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: "example_reader"},
			Endpoint: stuck, MaxAge: time.Minute})
	}()
	<-held
	defer func() {
		close(release)
		<-done
	}()
	before := w.broker.Counters()

	download := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("database bytes"))
	}))
	defer download.Close()
	facility := fetch.New("example", filepath.Join(t.TempDir(), "example"), []string{download.URL}, 5*time.Second,
		http.DefaultTransport)
	started := time.Now()
	result := facility.Fetch(context.Background(), fetch.Request{URL: download.URL + "/example.mmdb",
		File: "example.mmdb", MaxBytes: 1 << 20})
	if result.Outcome != fetch.OutcomeFetched || time.Since(started) > 2*time.Second {
		t.Errorf("the download ended %s after %v while the broker was saturated", result.Outcome, time.Since(started))
	}
	after := w.broker.Counters()
	if after.Calls != before.Calls || len(after.PerEndpoint) != len(before.PerEndpoint) {
		t.Errorf("the download is counted by the broker: %+v, then %+v", before, after)
	}
	var mirrored int
	if err := w.store.DB().QueryRow(`SELECT count(*) FROM opnsense.mirror_endpoint WHERE path LIKE '%example.mmdb%'`).
		Scan(&mirrored); err != nil || mirrored != 0 {
		t.Errorf("the download is in the mirror: %d rows (%v)", mirrored, err)
	}
}
