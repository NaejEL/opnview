package mmdbwrite

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"math"
	"net/netip"
	"sort"
)

// Record is one network and the record the database answers for it. A value is a string, a
// float64 (a double), a uint16, a uint32, a uint64, a map[string]any or a []any.
type Record struct {
	Network netip.Prefix
	Data    map[string]any
}

// Build returns a database of one type and build holding the records.
func Build(databaseType string, build uint64, records []Record) ([]byte, error) {
	var data bytes.Buffer
	offsets := make([]int, len(records))
	for index, record := range records {
		offsets[index] = data.Len()
		if err := encodeValue(&data, record.Data); err != nil {
			return nil, err
		}
	}

	// The search tree, as a trie over 128 bits. -1 is empty, -(2+i) is record i's data.
	type node struct{ records [2]int }
	nodes := []node{{records: [2]int{-1, -1}}}
	for index, record := range records {
		network := record.Network
		if !network.IsValid() || network.Bits() == 0 {
			return nil, fmt.Errorf("mmdbwrite: %v is not a network a record can be written under", network)
		}
		bits := network.Bits()
		raw := network.Addr().As16()
		if network.Addr().Is4() {
			// MaxMind places IPv4 in the first 2^32 addresses of the IPv6 space, not in the
			// IPv4-mapped block.
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
			if record >= 1<<24 {
				return nil, fmt.Errorf("mmdbwrite: the database is too large for 24-bit records")
			}
			tree.Write([]byte{byte(record >> 16), byte(record >> 8), byte(record)})
		}
	}

	var database bytes.Buffer
	database.Write(tree.Bytes())
	database.Write(make([]byte, 16))
	database.Write(data.Bytes())
	database.WriteString("\xab\xcd\xefMaxMind.com")
	if err := encodeValue(&database, map[string]any{
		"node_count":                  uint32(nodeCount),
		"record_size":                 uint16(24),
		"ip_version":                  uint16(6),
		"database_type":               databaseType,
		"languages":                   []any{"en"},
		"binary_format_major_version": uint16(2),
		"binary_format_minor_version": uint16(0),
		"build_epoch":                 build,
		"description":                 map[string]any{"en": "opnview test database"},
	}); err != nil {
		return nil, err
	}
	return database.Bytes(), nil
}

// Archive wraps a database in the gzip tar archive MaxMind serves: a directory named after the
// edition and the release, holding a COPYRIGHT.txt and <edition>.mmdb.
func Archive(edition, release string, database []byte) ([]byte, error) {
	var compressed bytes.Buffer
	zipped := gzip.NewWriter(&compressed)
	archive := tar.NewWriter(zipped)
	directory := edition + "_" + release + "/"
	for _, entry := range []struct {
		name string
		body []byte
	}{
		{directory + "COPYRIGHT.txt", []byte("opnview test archive")},
		{directory + edition + ".mmdb", database},
	} {
		if err := archive.WriteHeader(&tar.Header{
			Name: entry.name, Mode: 0o644, Size: int64(len(entry.body)), Typeflag: tar.TypeReg,
		}); err != nil {
			return nil, fmt.Errorf("mmdbwrite: writing the archive header: %w", err)
		}
		if _, err := archive.Write(entry.body); err != nil {
			return nil, fmt.Errorf("mmdbwrite: writing the archive: %w", err)
		}
	}
	if err := archive.Close(); err != nil {
		return nil, fmt.Errorf("mmdbwrite: closing the archive: %w", err)
	}
	if err := zipped.Close(); err != nil {
		return nil, fmt.Errorf("mmdbwrite: closing the gzip stream: %w", err)
	}
	return compressed.Bytes(), nil
}

// encodeValue writes one value in the MaxMind DB data format.
func encodeValue(out *bytes.Buffer, value any) error {
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
			if err := encodeValue(out, key); err != nil {
				return err
			}
			if err := encodeValue(out, typed[key]); err != nil {
				return err
			}
		}
	case []any:
		writeControl(out, 11, len(typed))
		for _, element := range typed {
			if err := encodeValue(out, element); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("mmdbwrite: a value of type %T cannot be written", value)
	}
	return nil
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

// writeControl writes a control byte, the extended type byte when the type needs one, and the
// size.
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

// FromScript converts a value a test script handed -- numbers as float64 or int64 -- into the types
// Build writes: a whole non-negative number is a uint32 when it fits and a uint64 otherwise, any
// other number a double.
func FromScript(value any) (any, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case int64:
		return unsignedOf(float64(typed), typed >= 0)
	case int:
		return unsignedOf(float64(typed), typed >= 0)
	case float64:
		if typed == math.Trunc(typed) && typed >= 0 && typed <= math.MaxUint32 {
			return uint32(typed), nil
		}
		return typed, nil
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, element := range typed {
			value, err := FromScript(element)
			if err != nil {
				return nil, err
			}
			converted[key] = value
		}
		return converted, nil
	case []any:
		converted := make([]any, 0, len(typed))
		for _, element := range typed {
			value, err := FromScript(element)
			if err != nil {
				return nil, err
			}
			converted = append(converted, value)
		}
		return converted, nil
	}
	return nil, fmt.Errorf("mmdbwrite: a value of type %T cannot be written", value)
}

// unsignedOf is a whole number's unsigned form.
func unsignedOf(value float64, nonNegative bool) (any, error) {
	if !nonNegative {
		return value, nil
	}
	if value <= math.MaxUint32 {
		return uint32(value), nil
	}
	return uint64(value), nil
}
