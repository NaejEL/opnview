package publicsuffix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// The probes the availability row records: what the last refresh found. The row
// describes the LIST, as geo_asn's describes the MaxMind dataset: it is reachable
// exactly when a parsed copy is held, whatever the last refresh found, because a
// failed refresh leaves the copy in use where it was.
const (
	// ProbeDownloaded is a refresh that downloaded a newer copy and put it in use.
	ProbeDownloaded = "list_downloaded"
	// ProbeNotModified is a refresh the server answered 304: the copy held is
	// current and nothing was downloaded.
	ProbeNotModified = "list_not_modified"
	// ProbeFailed is a refresh that could not fetch the list.
	ProbeFailed = "download_failed"
	// ProbeMalformed is a refresh that fetched something that is not the list.
	ProbeMalformed = "download_malformed"
)

// The files of the copy on disk, in the directory the refresher is given.
const (
	listFile     = "public_suffix_list.dat"
	metadataFile = "public_suffix_list.json"
)

// metadata is what is kept beside the copy: the validators the server sent with it,
// and the last instant a refresh confirmed it current.
type metadata struct {
	Validators  Validators `json:"validators"`
	RefreshedAt int64      `json:"refreshed_at"`
}

// Refresher keeps the copy current, records where it stands, and answers for it.
type Refresher struct {
	client *Client
	dir    string
	store  *store.Store
	now    func() time.Time

	mutex sync.RWMutex
	list  *List
	meta  metadata
}

// NewRefresher returns a refresher holding whatever copy dir already has. A copy on
// disk that does not parse is not an error that stops the service: it is reported,
// and the next refresh downloads the list again.
func NewRefresher(client *Client, dir string, database *store.Store, now func() time.Time) (*Refresher, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("publicsuffix: creating the list directory: %w", err)
	}
	refresher := &Refresher{client: client, dir: dir, store: database, now: now}
	raw, err := os.ReadFile(filepath.Join(dir, listFile))
	if errors.Is(err, os.ErrNotExist) {
		return refresher, nil
	}
	if err != nil {
		return refresher, fmt.Errorf("publicsuffix: reading the copy on disk: %w", err)
	}
	list, err := Parse(raw)
	if err != nil {
		return refresher, fmt.Errorf("publicsuffix: the copy on disk does not parse: %w", err)
	}
	refresher.list = list
	if encoded, err := os.ReadFile(filepath.Join(dir, metadataFile)); err == nil {
		_ = json.Unmarshal(encoded, &refresher.meta)
	}
	return refresher, nil
}

// List returns the copy in use, or nil when there is none.
func (r *Refresher) List() *List {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.list
}

// Refresh asks for the list, conditionally on the copy held, and puts a newer copy
// in use only once it parses. A failure keeps the copy in use and records a
// collection gap; every outcome is recorded as the provider's availability.
func (r *Refresher) Refresh(ctx context.Context) error {
	now := r.now().UTC().Unix()
	r.mutex.RLock()
	held, meta := r.list, r.meta
	r.mutex.RUnlock()

	validators := Validators{}
	if held != nil {
		validators = meta.Validators
	}
	fetched, err := r.client.Fetch(ctx, validators)
	probe := ProbeDownloaded
	var failure error
	switch {
	case err != nil:
		probe, failure = ProbeFailed, err
	case fetched.NotModified:
		probe = ProbeNotModified
		r.setMetadata(metadata{Validators: validators, RefreshedAt: now})
	default:
		list, parseErr := Parse(fetched.Body)
		if parseErr != nil {
			probe, failure = ProbeMalformed, parseErr
			break
		}
		if err := r.install(fetched.Body, metadata{Validators: fetched.Validators, RefreshedAt: now}); err != nil {
			probe, failure = ProbeFailed, err
			break
		}
		r.mutex.Lock()
		r.list = list
		r.mutex.Unlock()
	}

	providerID, err := r.store.ProviderID(ctx, Kind, ProviderKey)
	if err != nil {
		return err
	}
	if failure != nil {
		start := now
		if refreshed := r.refreshedAt(); refreshed > 0 && refreshed < now {
			start = refreshed
		}
		detail := failure.Error()
		if err := r.store.RecordCollectionGap(ctx, store.CollectionGap{
			ProviderID:      providerID,
			IntervalStartAt: start,
			IntervalEndAt:   now,
			Reason:          store.GapDownloadFailed,
			Detail:          &detail,
			DetectedAt:      now,
		}); err != nil {
			return err
		}
	}
	if err := r.record(ctx, providerID, probe, now); err != nil {
		return err
	}
	return failure
}

// install writes a new copy beside the one in use and renames it into place, so a
// copy on disk is always whole.
func (r *Refresher) install(body []byte, meta metadata) error {
	candidate, err := os.CreateTemp(r.dir, ".candidate-*")
	if err != nil {
		return fmt.Errorf("publicsuffix: creating the new copy: %w", err)
	}
	_, writeErr := candidate.Write(body)
	closeErr := candidate.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(candidate.Name())
		return fmt.Errorf("publicsuffix: writing the new copy: %w", writeErr)
	}
	if err := os.Rename(candidate.Name(), filepath.Join(r.dir, listFile)); err != nil {
		_ = os.Remove(candidate.Name())
		return fmt.Errorf("publicsuffix: moving the new copy into place: %w", err)
	}
	r.setMetadata(meta)
	return nil
}

// setMetadata keeps the metadata in memory and on disk. A failure to write it costs
// only a conditional request, so it is not a failure of the refresh.
func (r *Refresher) setMetadata(meta metadata) {
	r.mutex.Lock()
	r.meta = meta
	r.mutex.Unlock()
	if encoded, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(filepath.Join(r.dir, metadataFile), encoded, 0o640)
	}
}

func (r *Refresher) refreshedAt() int64 {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.meta.RefreshedAt
}

// record writes the availability row and the activation: reachable, and active,
// exactly when a copy is in use.
func (r *Refresher) record(ctx context.Context, providerID int64, probe string, now int64) error {
	list := r.List()
	state := store.StateUnavailable
	var detail *string
	if list != nil {
		state = store.StateReachable
		version := fmt.Sprintf("%d rules", list.Rules())
		if list.Version != "" {
			version = "VERSION " + list.Version + ", " + version
		}
		detail = &version
	}
	if err := r.store.SetAvailability(ctx, providerID, state, probe, detail, now); err != nil {
		return err
	}
	if list != nil {
		return r.store.SetActiveProviders(ctx, Kind, ProviderKey)
	}
	return r.store.SetActiveProviders(ctx, Kind)
}

// Tasks returns the refresh loop, at the interval the live configuration carries,
// asked again before every wait.
func Tasks(settings *config.Live, refresher *Refresher) []collect.Task {
	return []collect.Task{
		{
			Name: "Public Suffix List refresh",
			Interval: settings.Interval(func(s config.Config) time.Duration {
				return s.PublicSuffixRefreshInterval
			}),
			Run:        refresher.Refresh,
			RunAtStart: true,
		},
	}
}
