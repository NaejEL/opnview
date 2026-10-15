package buildinfo

// AppName is the name of the program.
const AppName = "opnview"

// DevelVersion is reported by Summary when no version was stamped into the
// binary, which is the case for every build made from a working tree.
const DevelVersion = "devel"

// Summary renders a one-line description of the running build. An empty or
// blank version is reported as DevelVersion rather than producing a dangling
// name, so a log line is never ambiguous about which build wrote it.
func Summary(version string) string {
	if isBlank(version) {
		version = DevelVersion
	}
	return AppName + " " + version
}

// isBlank reports whether s contains nothing but ASCII whitespace.
func isBlank(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			continue
		default:
			return false
		}
	}
	return true
}
