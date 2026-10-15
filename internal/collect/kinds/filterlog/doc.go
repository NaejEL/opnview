// Package filterlog is the coordinator of the firewall_log kind: the pulled pages of the filter
// log, the lines pushed over syslog, and the source periods that keep the two paths from storing
// one record twice. internal/collect composes it into the Collector.
package filterlog
