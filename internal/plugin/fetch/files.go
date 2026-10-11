package fetch

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/NaejEL/opnview/internal/suffixlist"
)

// The file helpers a plugin's script is given over its own data directory: generic file formats,
// each written once here rather than in every script.
//
// MMDB AND THE SUFFIX-LIST FORMAT ARE HOST HELPERS FOR ONE REASON EACH. MMDB is an open format
// that MaxMind, DB-IP and IPinfo all publish, so a reader of it is not one product's code, and a
// lookup in the interpreter would make every placing pass far slower. The suffix-list format is the
// Public Suffix List's own published format, and matching thousands of rules in the interpreter on
// every widget request would make every grouping slow. Both are opened once per file version and
// cached, so a script that opens one on every call pays for it once.

// maxTextBytes bounds a text a script reads whole: a validator, a recorded date, a small manifest
// of its own. A large file is read line by line, or opened with a format helper.
const maxTextBytes = 1 << 20

// Exists reports whether a file of the data directory exists.
func (f *Facility) Exists(name string) bool {
	path, err := f.Path(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// ReadText reads a small file of the data directory whole.
func (f *Facility) ReadText(name string) (string, error) {
	path, err := f.Path(name)
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxTextBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxTextBytes {
		return "", fmt.Errorf("fetch: %s is larger than %d bytes; read it line by line", name, maxTextBytes)
	}
	return string(raw), nil
}

// WriteText writes a small file of the data directory, whole or not at all.
func (f *Facility) WriteText(name, text string) error {
	path, err := f.Path(name)
	if err != nil {
		return err
	}
	if len(text) > maxTextBytes {
		return fmt.Errorf("fetch: a text written whole is at most %d bytes", maxTextBytes)
	}
	if err := os.MkdirAll(f.dir, 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(f.dir, ".fetch-*")
	if err != nil {
		return err
	}
	_, writeErr := temporary.WriteString(text)
	closeErr := temporary.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(temporary.Name())
		return writeErr
	}
	return os.Rename(temporary.Name(), path)
}

// Remove removes a file of the data directory, and the validators kept beside it when it was
// fetched; a file that is not there is not an error.
func (f *Facility) Remove(name string) error {
	path, err := f.Path(name)
	if err != nil {
		return err
	}
	forget(path)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(validatorsPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Replace puts a temporary file the facility wrote in place of a name, atomically.
func (f *Facility) Replace(temporary, name string) error {
	from, err := f.Path(temporary)
	if err != nil {
		return err
	}
	to, err := f.Path(name)
	if err != nil {
		return err
	}
	forget(from)
	forget(to)
	return os.Rename(from, to)
}

// Lines reads a file of the data directory line by line, at most limit lines.
func (f *Facility) Lines(name string, limit int) ([]string, error) {
	path, err := f.Path(name)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	var lines []string
	for scanner.Scan() {
		if limit > 0 && len(lines) >= limit {
			break
		}
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// SHA256 is the hexadecimal SHA-256 digest of a file of the data directory.
func (f *Facility) SHA256(name string) (string, error) {
	path, err := f.Path(name)
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// ExtractTarGz writes the regular file of a gzip tar archive whose base name is entry to a new
// temporary file of the data directory, and returns that file's name; the caller checks it and puts
// it in place with Replace, or leaves it to be removed.
func (f *Facility) ExtractTarGz(archive, entry string, maxBytes int64) (string, error) {
	path, err := f.Path(archive)
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("fetch: %s is not a gzip archive: %w", archive, err)
	}
	defer func() { _ = unzipped.Close() }()
	entries := tar.NewReader(unzipped)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("fetch: %s holds no %s", archive, entry)
		}
		if err != nil {
			return "", fmt.Errorf("fetch: reading %s: %w", archive, err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != entry {
			continue
		}
		return f.writeTemporary(entries, maxBytes)
	}
}

// Gunzip writes the decompressed content of a gzip file to a new temporary file and returns its
// name.
func (f *Facility) Gunzip(name string, maxBytes int64) (string, error) {
	path, err := f.Path(name)
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("fetch: %s is not a gzip file: %w", name, err)
	}
	defer func() { _ = unzipped.Close() }()
	return f.writeTemporary(unzipped, maxBytes)
}

// writeTemporary writes at most maxBytes of a reader to a new temporary file.
func (f *Facility) writeTemporary(from io.Reader, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		return "", errors.New("fetch: an extraction states the most bytes it may write")
	}
	temporary, err := os.CreateTemp(f.dir, ".extract-*")
	if err != nil {
		return "", err
	}
	written, err := io.Copy(temporary, io.LimitReader(from, maxBytes+1))
	closeErr := temporary.Close()
	if err == nil && written > maxBytes {
		err = fmt.Errorf("fetch: the entry is larger than %d bytes", maxBytes)
	}
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary.Name())
		return "", err
	}
	return filepath.Base(temporary.Name()), nil
}

// The format readers, opened once per file version.
var opened = struct {
	sync.Mutex
	mmdb  map[string]*cachedMMDB
	lists map[string]*cachedList
}{mmdb: map[string]*cachedMMDB{}, lists: map[string]*cachedList{}}

// forget drops what is cached of a path the facility is about to replace or remove. A file's version
// alone cannot tell two files apart that have the same size and were written within one tick of the
// kernel's coarse file clock, which two builds of one database written in quick succession are.
func forget(path string) {
	opened.Lock()
	defer opened.Unlock()
	delete(opened.mmdb, path)
	delete(opened.lists, path)
}

type version struct {
	modified int64
	size     int64
}

func versionOf(path string) (version, error) {
	info, err := os.Stat(path)
	if err != nil {
		return version{}, err
	}
	return version{modified: info.ModTime().UnixNano(), size: info.Size()}, nil
}

type cachedMMDB struct {
	version version
	reader  *maxminddb.Reader
}

type cachedList struct {
	version version
	list    *suffixlist.List
}

// MMDB is an open MaxMind DB format file.
type MMDB struct {
	reader *maxminddb.Reader
}

// OpenMMDB opens a file of the data directory in the MaxMind DB format. verify asks for the whole
// file to be checked, as the reader's own documentation asks of a file from outside: once per
// download, not per lookup.
func (f *Facility) OpenMMDB(name string, verify bool) (*MMDB, error) {
	path, err := f.Path(name)
	if err != nil {
		return nil, err
	}
	current, err := versionOf(path)
	if err != nil {
		return nil, err
	}
	opened.Lock()
	defer opened.Unlock()
	if cached, present := opened.mmdb[path]; present && cached.version == current && !verify {
		return &MMDB{reader: cached.reader}, nil
	}
	// THE FILE IS READ INTO MEMORY RATHER THAN MAPPED, AND A READER IS NEVER CLOSED. A lookup may be
	// running on a reader while a refresh replaces its file; closing a mapped reader under it would
	// unmap the memory the lookup reads. A reader read from memory is freed by the garbage collector
	// once no lookup holds it.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fetch: reading %s: %w", name, err)
	}
	reader, err := maxminddb.OpenBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("fetch: opening %s: %w", name, err)
	}
	if verify {
		if err := reader.Verify(); err != nil {
			return nil, fmt.Errorf("fetch: %s does not verify: %w", name, err)
		}
	}
	opened.mmdb[path] = &cachedMMDB{version: current, reader: reader}
	return &MMDB{reader: reader}, nil
}

// DatabaseType is the file's own metadata type, such as GeoLite2-City.
func (m *MMDB) DatabaseType() string { return m.reader.Metadata.DatabaseType }

// BuildEpoch is the file's build, in Unix seconds.
func (m *MMDB) BuildEpoch() int64 { return int64(m.reader.Metadata.BuildEpoch) }

// Lookup returns the record of an address, decoded to JSON-shaped values, and whether there is one.
func (m *MMDB) Lookup(address string) (any, bool, error) {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return nil, false, fmt.Errorf("fetch: %q is not an address", address)
	}
	result := m.reader.Lookup(parsed.Unmap())
	if !result.Found() {
		return nil, false, result.Err()
	}
	var record any
	if err := result.Decode(&record); err != nil {
		return nil, false, err
	}
	return jsonShaped(record), true, nil
}

// jsonShaped turns a decoded MMDB value into the shapes encoding/json produces: maps with string
// keys, slices, float64 numbers, strings and booleans.
func jsonShaped(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, element := range typed {
			typed[key] = jsonShaped(element)
		}
		return typed
	case []any:
		for index, element := range typed {
			typed[index] = jsonShaped(element)
		}
		return typed
	case uint64:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case int:
		return float64(typed)
	case float32:
		return float64(typed)
	}
	return value
}

// OpenSuffixList opens a file of the data directory in the Public Suffix List format.
func (f *Facility) OpenSuffixList(name string) (*suffixlist.List, error) {
	path, err := f.Path(name)
	if err != nil {
		return nil, err
	}
	current, err := versionOf(path)
	if err != nil {
		return nil, err
	}
	opened.Lock()
	defer opened.Unlock()
	if cached, present := opened.lists[path]; present && cached.version == current {
		return cached.list, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	list, err := suffixlist.Parse(raw)
	if err != nil {
		return nil, err
	}
	opened.lists[path] = &cachedList{version: current, list: list}
	return list, nil
}

func sortStrings(values []string) { sort.Strings(values) }
