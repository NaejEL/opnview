// Package scripthost binds a plugin's JavaScript to the host: the interpreter a script runs in
// (script.go); the host functions it calls, through the broker and the observed answers
// (hostbroker.go, hostobserver.go, observed.go, hostextras.go); the script variant chosen for the
// firewall's version (scriptversions.go); and the conversion of what a script returns into a
// kind's records (convert.go).
package scripthost
