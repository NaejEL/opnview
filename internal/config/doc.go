// Package config is the only reader of configuration in opnview.
//
// There are exactly two sources, and no third: the `setting` table, and the
// defaults written down here. There is no environment variable, no
// configuration file and no `configure` subcommand — the firewall URL, the API
// key and secret and the MaxMind key are entered in opnview's own settings
// surface and nowhere else.
//
// Since cycle 4B this package also SURFACES THE CREDENTIALS TO THE COLLECTORS and
// notices a change to them without a restart. That is credentials.go: the
// `setting` rows holding the firewall URL and the API key, the decryption of the
// API secret from `encrypted_credential`, the three named states those can be in,
// and the live holder the OPNsense client reads on every call. live.go does the
// same for the rest of the configuration.
//
// Every default here is a duration, a mode or a page size. NO INTERFACE NAME,
// VLAN NAME, ADDRESS, CIDR OR COUNT OF THINGS ON THE NETWORK IS A DEFAULT, because
// every one of those is discovered at runtime through the API.
package config
