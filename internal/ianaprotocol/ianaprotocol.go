// Package ianaprotocol holds the keywords of IANA's Assigned Internet Protocol Numbers, so a protocol
// the filter log names can be compared with one an exporter numbers (PS17 of the NetFlow join
// amendment to specs/SPEC-backend-completion-plugins.md: "Protocol is compared by IANA number").
//
// THIS IS A STANDARD, NOT INSTALLATION DATA, like internal/push/ipfixregistry. protocol-numbers.csv
// is the registry's decimal number and keyword, transcribed from
//
//	https://www.iana.org/assignments/protocol-numbers/protocol-numbers-1.csv
//
// retrieved on 11 October 2026, with every other column removed and the ranges and the numbers with
// no keyword dropped.
//
// WHY A KEYWORD MAPS. The filter log stores the protocol's NAME: OPNsense's filterlog writes
// getprotobynumber(3)'s p_name (opnsense/ports 26.7.3, opnsense/filterlog/files/print-ip.c and
// print-ip6.c), and the FreeBSD protocols database it reads names each protocol by IANA's keyword
// in lower case -- tcp, udp, icmp, ipv6-icmp (opnsense/src stable/26.7, lib/libc/net/protocols). A
// name is therefore matched to its number case-insensitively, the registry's "(deprecated)" mark
// set aside. A name the registry does not hold -- FreeBSD's `carp` for 112, which IANA calls VRRP --
// has no number, and a record of it compares equal to nothing: never guessed.
package ianaprotocol

import (
	"bufio"
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

//go:embed protocol-numbers.csv
var numbersFile string

var (
	once     sync.Once
	byName   map[string]int64
	byNumber map[int64]string
)

func load() {
	byName, byNumber = map[string]int64{}, map[int64]string{}
	scanner := bufio.NewScanner(strings.NewReader(numbersFile))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		number, keyword, found := strings.Cut(line, ",")
		if !found {
			continue
		}
		value, err := strconv.ParseInt(number, 10, 64)
		if err != nil {
			continue
		}
		keyword = strings.TrimSpace(strings.TrimSuffix(keyword, " (deprecated)"))
		byName[strings.ToLower(keyword)] = value
		byNumber[value] = keyword
	}
}

// Number returns the IANA protocol number of a name, compared with the registry's keywords
// case-insensitively; ok is false for a name the registry does not hold.
func Number(name string) (int64, bool) {
	once.Do(load)
	number, found := byName[strings.ToLower(strings.TrimSpace(name))]
	return number, found
}

// Keyword returns the registry's keyword of a number; ok is false for a number with none.
func Keyword(number int64) (string, bool) {
	once.Do(load)
	keyword, found := byNumber[number]
	return keyword, found
}
