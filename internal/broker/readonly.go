package broker

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// Read-only, enforced for every plugin and for the core (A13 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md).
//
// WHAT IS FORWARDED: a GET, and a POST to a read action -- a command, the fourth path element,
// compared in its snake_case spelling as MutatingCommand compares it, that starts with search,
// get, query or list, or ends with status. EVERYTHING ELSE IS REFUSED BEFORE A REQUEST OBJECT
// EXISTS, and MutatingCommand is checked first and stays on top: carp_status ends in status and
// changes the CARP state on a POST, and is refused all the same.
//
// THIS IS NOT A SANDBOX, AN ALLOWLIST OF PLUGINS OR AN ACTIVATION STEP. Plugins stay free in
// everything else; the API key should still hold read privileges only (README.md).

// CodeWriteRefused is the closed code of a refused write, logged with the plugin's name and the
// path, and counted in the API catalogue.
const CodeWriteRefused = "write_refused"

// ErrWriteRefused is a request the broker refused as a write.
type ErrWriteRefused struct {
	Requester string
	Method    string
	Path      string
	Reason    string
}

func (e *ErrWriteRefused) Error() string {
	return fmt.Sprintf("broker: %s: %s %s by %s refused: %s", CodeWriteRefused, e.Method, e.Path, e.Requester, e.Reason)
}

// readVerbs are the prefixes of a read action's command.
var readVerbs = []string{"search", "get", "query", "list"}

// ReadAction reports whether a command is a read action a POST may reach.
func ReadAction(command string) bool {
	snake := snakeCase(command)
	for _, verb := range readVerbs {
		if strings.HasPrefix(snake, verb) {
			return true
		}
	}
	return strings.HasSuffix(snake, "status")
}

// command is a path's fourth element: the action the OPNsense router reaches.
func command(path string) string {
	elements := strings.Split(strings.Trim(path, "/"), "/")
	if len(elements) < 4 {
		return ""
	}
	return elements[3]
}

// CheckRead applies the rule to one request: nil when the broker forwards it.
func CheckRead(method, path string) string {
	if mutating := opnsense.MutatingCommand(path); mutating != "" {
		return fmt.Sprintf("the command %q is one opnview never issues", mutating)
	}
	switch method {
	case http.MethodGet:
		return ""
	case http.MethodPost:
		if action := command(path); ReadAction(action) {
			return ""
		}
		return fmt.Sprintf("a POST reaches a read action only (a command starting with search, get, query or "+
			"list, or ending with status), and %q is none", command(path))
	}
	return fmt.Sprintf("the method %s is not GET or POST", method)
}

// snakeCase is the router's other spelling of a camelCase command, as MutatingCommand reads it.
func snakeCase(command string) string {
	var out strings.Builder
	for index, character := range command {
		if character >= 'A' && character <= 'Z' {
			if index > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(character + ('a' - 'A'))
			continue
		}
		out.WriteRune(character)
	}
	return out.String()
}
