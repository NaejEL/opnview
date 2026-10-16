// Package installation holds the script host's areas that read the installation's own state for a
// widget's script: the installation and its interfaces, the current rates and the measurement
// samples, the data catalogue's datasets, and the widget's own statements. Each area is a file that
// registers itself from its own init() (script.RegisterHostArea); internal/widget links the
// package.
package installation
