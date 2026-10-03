package maxmind

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"math"
	"net/netip"
	"sort"
	"testing"
)

// A minimal MaxMind DB writer, for tests only: an IPv6 search tree with 24-bit
// records, IPv4 networks placed under ::/96 as MaxMind's own databases place them,
// and the data types the City and ASN records use. It follows the published MaxMind
// DB format, version 2.0, and nothing in it is read from a real database: every
// record a test looks up is one the test wrote.

// testRecord is one network and the record the database answers for it.
type testRecord struct {
	network netip.Prefix
	data    map[string]any
}

// buildDatabase returns a database of one type and build holding the records.
func buildDatabase(t *testing.T, databaseType string, build uint64, records []testRecord) []byte {
	t.Helper()

	// The data section: one encoded record per network.
	var data bytes.Buffer
	offsets := make([]int, len(records))
	for index, record := range records {
		offsets[index] = data.Len()
		encodeValue(t, &data, record.data)
	}

	// The search tree, as a trie over 128 bits.
	type node struct{ records [2]int } // -1 is empty, -(2+i) is record i's data
	nodes := []node{{records: [2]int{-1, -1}}}
	for index, record := range records {
		network := record.network
		bits := network.Bits()
		raw := network.Addr().As16()
		if network.Addr().Is4() {
			// MaxMind places IPv4 under ::/96, not under the ::ffff:0:0/96 mapping.
			v4 := network.Addr().As4()
			raw = [16]byte{}
			copy(raw[12:], v4[:])
			bits += 96
		}
		current := 0
		for depth := 0; depth < bits; depth++ {
			bit := int(raw[depth/8]>>(7-uint(depth%8))) & 1
			if depth == bits-1 {
				nodes[current].records[bit] = -(2 + index)
				break
			}
			next := nodes[current].records[bit]
			if next < 0 {
				nodes = append(nodes, node{records: [2]int{-1, -1}})
				next = len(nodes) - 1
				nodes[current].records[bit] = next
			}
			current = next
		}
	}

	nodeCount := len(nodes)
	var tree bytes.Buffer
	for _, n := range nodes {
		for _, value := range n.records {
			var record int
			switch {
			case value == -1:
				record = nodeCount
			case value <= -2:
				record = nodeCount + 16 + offsets[-value-2]
			default:
				record = value
			}
			tree.Write([]byte{byte(record >> 16), byte(record >> 8), byte(record)})
		}
	}

	var database bytes.Buffer
	database.Write(tree.Bytes())
	database.Write(make([]byte, 16))
	database.Write(data.Bytes())
	database.WriteString("\xab\xcd\xefMaxMind.com")
	encodeValue(t, &database, map[string]any{
		"node_count":                  uint32(nodeCount),
		"record_size":                 uint16(24),
		"ip_version":                  uint16(6),
		"database_type":               databaseType,
		"languages":                   []any{"en"},
		"binary_format_major_version": uint16(2),
		"binary_format_minor_version": uint16(0),
		"build_epoch":                 build,
		"description":                 map[string]any{"en": "opnview test database"},
	})
	return database.Bytes()
}

// encodeValue writes one value in the MaxMind DB data format.
func encodeValue(t *testing.T, out *bytes.Buffer, value any) {
	t.Helper()
	switch typed := value.(type) {
	case string:
		writeControl(out, 2, len(typed))
		out.WriteString(typed)
	case float64:
		writeControl(out, 3, 8)
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], math.Float64bits(typed))
		out.Write(raw[:])
	case uint16:
		writeUnsigned(out, 5, uint64(typed))
	case uint32:
		writeUnsigned(out, 6, uint64(typed))
	case uint64:
		writeUnsigned(out, 9, typed)
	case map[string]any:
		writeControl(out, 7, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			encodeValue(t, out, key)
			encodeValue(t, out, typed[key])
		}
	case []any:
		writeControl(out, 11, len(typed))
		for _, element := range typed {
			encodeValue(t, out, element)
		}
	default:
		t.Fatalf("the test writer cannot encode %T", value)
	}
}

// writeUnsigned writes an unsigned integer in as few bytes as it needs.
func writeUnsigned(out *bytes.Buffer, kind int, value uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	start := 0
	for start < 8 && raw[start] == 0 {
		start++
	}
	writeControl(out, kind, 8-start)
	out.Write(raw[start:])
}

// writeControl writes a control byte, the extended type byte when the type needs
// one, and the size.
func writeControl(out *bytes.Buffer, kind, size int) {
	var control byte
	extended := kind > 7
	if !extended {
		control = byte(kind) << 5
	}
	var tail []byte
	switch {
	case size < 29:
		control |= byte(size)
	case size < 29+256:
		control |= 29
		tail = []byte{byte(size - 29)}
	default:
		control |= 30
		rest := size - 285
		tail = []byte{byte(rest >> 8), byte(rest)}
	}
	out.WriteByte(control)
	if extended {
		out.WriteByte(byte(kind - 7))
	}
	out.Write(tail)
}

// archiveOf wraps a database in the gzip tar archive MaxMind serves, under the
// directory name MaxMind's archives use.
func archiveOf(t *testing.T, edition Edition, database []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zipped := gzip.NewWriter(&compressed)
	archive := tar.NewWriter(zipped)
	name := string(edition) + "_20261003/" + string(edition) + ".mmdb"
	for _, entry := range []struct {
		name string
		body []byte
	}{
		{string(edition) + "_20261003/COPYRIGHT.txt", []byte("test")},
		{name, database},
	} {
		if err := archive.WriteHeader(&tar.Header{
			Name: entry.name, Mode: 0o644, Size: int64(len(entry.body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("writing the archive header: %v", err)
		}
		if _, err := archive.Write(entry.body); err != nil {
			t.Fatalf("writing the archive: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("closing the archive: %v", err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatalf("closing the gzip stream: %v", err)
	}
	return compressed.Bytes()
}
