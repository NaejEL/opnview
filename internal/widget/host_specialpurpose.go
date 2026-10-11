package widget

import (
	"net/netip"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/specialpurpose"
)

// host.specialpurpose: the IANA special-purpose address registries (internal/specialpurpose), so a
// widget says whether an address is public, or lies in a range the Internet never routes to it, by
// arithmetic on the address and never by a guess about a name.
//
//	host.specialpurpose.classify(address)  {special_purpose, public}: special_purpose is the most
//	                                       specific registry block holding the address -- {block, name,
//	                                       reference, globally_reachable}, globally_reachable null where
//	                                       the registry gives no value -- or null for an address in no
//	                                       block; public is whether the Internet can reach it. A text
//	                                       that is not an address is in no block and is not public.
//	host.specialpurpose.registries         the two registries the blocks are read from, cited by name

// specialPurposeBlock is the registry block an address lies in, as a script reads it.
type specialPurposeBlock struct {
	Block             string `json:"block"`
	Name              string `json:"name"`
	Reference         string `json:"reference"`
	GloballyReachable *bool  `json:"globally_reachable"`
}

// specialPurposeClass is what the registries say of one address, as a script reads it.
type specialPurposeClass struct {
	SpecialPurpose *specialPurposeBlock `json:"special_purpose"`
	Public         bool                 `json:"public"`
}

func init() {
	registerHostArea("specialpurpose", func(call *scriptCall, area *goja.Object) {
		call.function(area, "classify", func(arguments goja.FunctionCall) goja.Value {
			text := call.textOf(arguments.Argument(0), "the address")
			class := specialPurposeClass{}
			parsed, err := netip.ParseAddr(text)
			if err != nil {
				return call.value(class)
			}
			if block, special := specialpurpose.Lookup(parsed); special {
				class.SpecialPurpose = &specialPurposeBlock{Block: block.Block.String(), Name: block.Name,
					Reference: block.Reference, GloballyReachable: block.GloballyReachable}
			}
			class.Public = specialpurpose.Public(parsed)
			return call.value(class)
		})
		_ = area.Set("registries", call.value([]string{"iana-ipv4-special-registry", "iana-ipv6-special-registry"}))
	})
}
