// Package harness runs a plugin's own tests: the JavaScript files of its tests/ directory, each
// against a world of its own -- a fake firewall, a fresh database and data directory, a web that
// answers only what the test arranged -- through the same loader, interpreter and adapters the
// service runs the plugin with (testrun.go); the push test API (testpush.go); and the widget test
// API (testwidgets.go). `opnview plugin test <directory>` and the Go suite's test of every built-in
// plugin both run here. It reaches the plugin host only through what internal/plugin exposes for it
// (harnesscalls.go). docs/plugins.md, "Testing a plugin", is the reference.
package harness
