package scripthost

import (
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

// The standard readers a script reaches under opnview beside the decoders: an IP address read as a value, and an
// IANA protocol keyword read as its number. Both are standards, never installation data.

// ipValue reads an IP address as a script compares it (opnview.ip): {address, version, key, loopback}, the
// address in its canonical text -- an IPv4-mapped IPv6 address read as the IPv4 address it maps, RFC 5952's
// text for IPv6 -- its version, a key whose text order is the addresses' order (IPv4 before IPv6, then by
// value), and whether it is a loopback address; null for a text that is no address. Two texts naming one
// address read the same, so a script compares addresses by value, never by how a source wrote them.
func ipValue(vm *goja.Runtime, text string) goja.Value {
	address, err := netip.ParseAddr(strings.TrimSpace(text))
	if err != nil {
		return goja.Null()
	}
	address = address.Unmap()
	version := int64(6)
	if address.Is4() {
		version = 4
	}
	bytes := address.AsSlice()
	answer := vm.NewObject()
	_ = answer.Set("address", address.String())
	_ = answer.Set("version", version)
	_ = answer.Set("key", strconv.FormatInt(version, 10)+":"+hex.EncodeToString(bytes)+"%"+address.Zone())
	_ = answer.Set("loopback", address.IsLoopback())
	return answer
}
