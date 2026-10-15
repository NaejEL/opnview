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
