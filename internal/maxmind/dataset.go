package maxmind

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Dataset is the two databases on disk, open for lookups.
//
// A FILE IS REPLACED ONLY BY ONE THAT OPENED. A new build is written beside the one in
// use, opened, and checked to be the edition it claims to be; only then is it renamed
// over the old file and swapped in. The old file is gone from the directory the
// moment it is replaced, which is how the GeoLite EULA's rule — destroy an old build
// within 30 days of a new release — is met by construction rather than by a sweep.
type Dataset struct {
	dir string

	mutex   sync.RWMutex
	readers map[Edition]*maxminddb.Reader
}

// OpenDataset opens whatever databases dir already holds. A database that is not
// there is not an error: it has not been downloaded yet.
//
// ONE THAT IS THERE AND DOES NOT OPEN DOES NOT STOP THE SERVICE. The dataset is
// returned without it, together with an error saying which file and why, for the
// caller to report: the next refresh finds that edition absent and downloads it
// again. Only a directory that cannot be created returns no dataset.
func OpenDataset(dir string) (*Dataset, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("maxmind: creating the database directory: %w", err)
	}
	dataset := &Dataset{dir: dir, readers: map[Edition]*maxminddb.Reader{}}
	var failures []error
	for _, edition := range editions() {
		path := dataset.path(edition)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		reader, err := openEdition(path, edition)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		dataset.readers[edition] = reader
	}
	return dataset, errors.Join(failures...)
}

// path is where one edition lives.
func (d *Dataset) path(edition Edition) string {
	return filepath.Join(d.dir, string(edition)+".mmdb")
}

// openEdition opens a database and checks that its metadata names the edition.
func openEdition(path string, edition Edition) (*maxminddb.Reader, error) {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("maxmind: opening %s: %w", path, err)
	}
	if reader.Metadata.DatabaseType != string(edition) {
		_ = reader.Close()
		return nil, fmt.Errorf("maxmind: %s holds %q, not %s", path,
			reader.Metadata.DatabaseType, edition)
	}
	return reader, nil
}

// Build is the build of one edition on disk, in Unix seconds, and whether there is
// one.
func (d *Dataset) Build(edition Edition) (int64, bool) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	reader, present := d.readers[edition]
	if !present {
		return 0, false
	}
	return int64(reader.Metadata.BuildEpoch), true
}

// Ready is whether both editions are open, and the build lookups are recorded
// against: the City build, which answers the location a row is resolved by.
func (d *Dataset) Ready() (int64, bool) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	city, cityPresent := d.readers[EditionCity]
	_, asnPresent := d.readers[EditionASN]
	if !cityPresent || !asnPresent {
		return 0, false
	}
	return int64(city.Metadata.BuildEpoch), true
}

// replace swaps the database at candidate in for one edition, after it opened and
// named that edition. On any failure the database in use is untouched and the
// candidate is removed.
func (d *Dataset) replace(edition Edition, candidate string) error {
	reader, err := openEdition(candidate, edition)
	if err != nil {
		_ = os.Remove(candidate)
		return err
	}
	// A downloaded file is checked whole before it is trusted, as the reader's own
	// documentation asks of a database from outside: once per download, not per lookup.
	if err := reader.Verify(); err != nil {
		_ = reader.Close()
		_ = os.Remove(candidate)
		return fmt.Errorf("maxmind: the new %s does not verify: %w", edition, err)
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if err := os.Rename(candidate, d.path(edition)); err != nil {
		_ = reader.Close()
		_ = os.Remove(candidate)
		return fmt.Errorf("maxmind: moving the new %s into place: %w", edition, err)
	}
	if previous, present := d.readers[edition]; present {
		_ = previous.Close()
	}
	d.readers[edition] = reader
	return nil
}

// Close closes both databases.
func (d *Dataset) Close() error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	var failures []error
	for edition, reader := range d.readers {
		if err := reader.Close(); err != nil {
			failures = append(failures, err)
		}
		delete(d.readers, edition)
	}
	return errors.Join(failures...)
}

// Answer is what the two databases say about one address. A field is nil when the
// database gave no value for it.
type Answer struct {
	CountryCode *string
	CountryName *string
	Latitude    *float64
	Longitude   *float64
	ASN         *int64
	Operator    *string
}

// cityRecord is the part of a City record opnview reads. The registered country is
// read as well, for an address whose own country the database leaves empty.
type cityRecord struct {
	Country           countryRecord `maxminddb:"country"`
	RegisteredCountry countryRecord `maxminddb:"registered_country"`
	Location          struct {
		Latitude  *float64 `maxminddb:"latitude"`
		Longitude *float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
}

// countryRecord is a country as the City database names it.
type countryRecord struct {
	ISOCode string            `maxminddb:"iso_code"`
	Names   map[string]string `maxminddb:"names"`
}

// asnRecord is an ASN record.
type asnRecord struct {
	Number       uint   `maxminddb:"autonomous_system_number"`
	Organization string `maxminddb:"autonomous_system_organization"`
}

// Lookup asks both databases about one address. It reads the files and contacts
// nothing. It answers false when the dataset is not ready.
func (d *Dataset) Lookup(address netip.Addr) (Answer, bool, error) {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	city, cityPresent := d.readers[EditionCity]
	asn, asnPresent := d.readers[EditionASN]
	if !cityPresent || !asnPresent {
		return Answer{}, false, nil
	}

	var answer Answer
	var place cityRecord
	if err := city.Lookup(address).Decode(&place); err != nil {
		return Answer{}, false, fmt.Errorf("maxmind: looking up %s in %s: %w", address, EditionCity, err)
	}
	country := place.Country
	if country.ISOCode == "" {
		country = place.RegisteredCountry
	}
	if country.ISOCode != "" {
		code := country.ISOCode
		answer.CountryCode = &code
		if name, present := country.Names["en"]; present && name != "" {
			answer.CountryName = &name
		}
	}
	answer.Latitude, answer.Longitude = place.Location.Latitude, place.Location.Longitude

	var system asnRecord
	if err := asn.Lookup(address).Decode(&system); err != nil {
		return Answer{}, false, fmt.Errorf("maxmind: looking up %s in %s: %w", address, EditionASN, err)
	}
	if system.Number != 0 {
		number := int64(system.Number)
		answer.ASN = &number
	}
	if system.Organization != "" {
		organization := system.Organization
		answer.Operator = &organization
	}
	return answer, true, nil
}
