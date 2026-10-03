package maxmind

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/store"
)

// The registry row this package implements.
const (
	// Kind is the registry kind.
	Kind = "geo_asn"
	// ProviderKey is the registry row of the MaxMind GeoLite2 databases.
	ProviderKey = "maxmind_geolite2"
)

// The probes the availability row records: what the last refresh found. The row
// describes the DATASET, not one address: it is reachable exactly when both
// databases are on disk and open, whatever the last refresh found, because a failed
// refresh leaves the databases in use where they were.
const (
	// ProbeCurrent is a refresh that found every edition current, or replaced it.
	ProbeCurrent = "dataset_current"
	// ProbeNoLicenceKey is no licence key stored: nothing was requested.
	ProbeNoLicenceKey = "no_licence_key"
	// ProbeLicenceKeyUndecryptable is a licence key stored and not openable.
	ProbeLicenceKeyUndecryptable = "licence_key_undecryptable"
	// ProbeNoAccountID is a licence key with no account ID beside it.
	ProbeNoAccountID = "no_account_id"
	// ProbeRefused is MaxMind refusing the account ID and the licence key.
	ProbeRefused = "download_refused"
	// ProbeLimited is the account's daily download limit spent.
	ProbeLimited = "download_limit_reached"
	// ProbeFailed is any other failure: the network, an unexpected answer, an
	// archive that does not hold a database that opens.
	ProbeFailed = "download_failed"
)

// CredentialSource reads the credentials at the moment of a refresh, so a key or an
// account ID saved since the last one is used without a restart.
type CredentialSource func(ctx context.Context) (config.MaxMindCredentials, config.CredentialState, error)

// Refresher keeps the dataset current and records where it stands.
type Refresher struct {
	client      *Client
	dataset     *Dataset
	store       *store.Store
	credentials CredentialSource
	now         func() time.Time
	wake        chan struct{}
}

// NewRefresher returns a refresher. now is the clock the availability row is stamped
// with.
func NewRefresher(client *Client, dataset *Dataset, database *store.Store,
	credentials CredentialSource, now func() time.Time) *Refresher {
	return &Refresher{
		client: client, dataset: dataset, store: database, credentials: credentials, now: now,
		wake: make(chan struct{}, 1),
	}
}

// Wake asks for a refresh now rather than at the next interval. It never blocks: a
// wake already pending is the same request.
func (r *Refresher) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Woken is what the refresh loop waits on beside its interval.
func (r *Refresher) Woken() <-chan struct{} { return r.wake }

// Refresh checks each edition and downloads the ones that changed, then records the
// dataset's state and activates the provider exactly when the dataset is ready.
//
// WITHOUT A LICENCE KEY OR AN ACCOUNT ID, NOTHING IS REQUESTED. The state says which
// is missing.
func (r *Refresher) Refresh(ctx context.Context) error {
	credentials, state, err := r.credentials(ctx)
	if err != nil {
		return err
	}
	probe := ProbeCurrent
	var failure error
	switch {
	case state == config.CredentialStateUndecryptable:
		probe = ProbeLicenceKeyUndecryptable
	case state != config.CredentialStateReady:
		probe = ProbeNoLicenceKey
	case credentials.AccountID == "":
		probe = ProbeNoAccountID
	default:
		for _, edition := range editions() {
			if err := r.refreshEdition(ctx, credentials, edition); err != nil {
				probe, failure = probeFor(err), err
				// A refusal or a spent limit is the same answer for the second edition;
				// asking again would only spend another request.
				break
			}
		}
	}
	if err := r.record(ctx, probe); err != nil {
		return err
	}
	return failure
}

// probeFor names a refresh failure.
func probeFor(err error) string {
	switch {
	case errors.Is(err, ErrRefused):
		return ProbeRefused
	case errors.Is(err, ErrLimited):
		return ProbeLimited
	default:
		return ProbeFailed
	}
}

// record writes the availability row and the activation.
func (r *Refresher) record(ctx context.Context, probe string) error {
	providerID, err := r.store.ProviderID(ctx, Kind, ProviderKey)
	if err != nil {
		return err
	}
	build, ready := r.dataset.Ready()
	state := store.StateUnavailable
	var detail *string
	if ready {
		state = store.StateReachable
		built := time.Unix(build, 0).UTC().Format(time.RFC3339)
		detail = &built
	}
	if err := r.store.SetAvailability(ctx, providerID, state, probe, detail,
		r.now().UTC().Unix()); err != nil {
		return err
	}
	if ready {
		return r.store.SetActiveProviders(ctx, Kind, ProviderKey)
	}
	return r.store.SetActiveProviders(ctx, Kind)
}

// refreshEdition downloads one edition when MaxMind has a build newer than the one
// downloaded last.
//
// THE COMPARISON IS AGAINST THE Last-Modified OF THE LAST DOWNLOAD, kept beside the
// database, and not against the build date inside it: a file is uploaded after it is
// built, so the two never agree, and comparing them would download every edition on
// every check.
func (r *Refresher) refreshEdition(ctx context.Context, credentials config.MaxMindCredentials,
	edition Edition) error {
	modified, err := r.client.LastModified(ctx, credentials, edition)
	if err != nil {
		return err
	}
	if _, present := r.dataset.Build(edition); present {
		if known, ok := r.lastModified(edition); ok && !modified.After(known) {
			return nil
		}
	}

	archive, err := os.CreateTemp(r.dataset.dir, ".download-*")
	if err != nil {
		return fmt.Errorf("maxmind: creating the %s download file: %w", edition, err)
	}
	defer func() { _ = os.Remove(archive.Name()) }()
	defer func() { _ = archive.Close() }()
	if err := r.client.Download(ctx, credentials, edition, archive); err != nil {
		return err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("maxmind: rereading the %s download: %w", edition, err)
	}
	candidate, err := extractDatabase(archive, r.dataset.dir, edition)
	if err != nil {
		return err
	}
	if err := r.dataset.replace(edition, candidate); err != nil {
		return err
	}
	return r.rememberLastModified(edition, modified)
}

// maxDatabaseBytes bounds what is extracted from an archive. GeoLite2 City is the
// larger edition, at tens of megabytes; this leaves an order of magnitude of room
// and stops a corrupt archive from filling the disk.
const maxDatabaseBytes = 1 << 30

// extractDatabase writes the edition's .mmdb out of a gzip tar archive into a new
// file in dir and returns its path.
func extractDatabase(archive io.Reader, dir string, edition Edition) (string, error) {
	unzipped, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("maxmind: the %s download is not a gzip archive: %w", edition, err)
	}
	defer func() { _ = unzipped.Close() }()
	entries := tar.NewReader(unzipped)
	want := string(edition) + ".mmdb"
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("maxmind: the %s archive holds no %s", edition, want)
		}
		if err != nil {
			return "", fmt.Errorf("maxmind: reading the %s archive: %w", edition, err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != want {
			continue
		}
		candidate, err := os.CreateTemp(dir, ".candidate-*")
		if err != nil {
			return "", fmt.Errorf("maxmind: creating the new %s: %w", edition, err)
		}
		written, err := io.Copy(candidate, io.LimitReader(entries, maxDatabaseBytes+1))
		closeErr := candidate.Close()
		if err == nil && written > maxDatabaseBytes {
			err = fmt.Errorf("maxmind: the %s database is larger than %d bytes", edition, maxDatabaseBytes)
		}
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(candidate.Name())
			return "", fmt.Errorf("maxmind: extracting the %s database: %w", edition, err)
		}
		return candidate.Name(), nil
	}
}

// lastModifiedPath is the file beside a database holding the Last-Modified of the
// download it came from.
func (r *Refresher) lastModifiedPath(edition Edition) string {
	return filepath.Join(r.dataset.dir, string(edition)+".last-modified")
}

// lastModified reads the Last-Modified of the last download of one edition.
func (r *Refresher) lastModified(edition Edition) (time.Time, bool) {
	raw, err := os.ReadFile(r.lastModifiedPath(edition))
	if err != nil {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}

// rememberLastModified records the Last-Modified of the download just installed.
func (r *Refresher) rememberLastModified(edition Edition, modified time.Time) error {
	if err := os.WriteFile(r.lastModifiedPath(edition),
		[]byte(strconv.FormatInt(modified.Unix(), 10)+"\n"), 0o640); err != nil {
		return fmt.Errorf("maxmind: recording the %s download date: %w", edition, err)
	}
	return nil
}

// Ready is the build of the databases in use, and whether both are open. It is what
// the settings surface shows beside the state.
func (r *Refresher) Ready() (int64, bool) { return r.dataset.Ready() }
