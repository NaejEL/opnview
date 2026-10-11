// The end-of-life ISC dhcpd plugin: one implementation of the dhcp_lease kind, registered, probed
// and never read.
//
// /api/dhcpv4/leases/searchLease answered 404 on a live 26.7.3_11, and the survey marks the
// plugin's parameter and field contract UNVERIFIED because it lives in the plugin repository
// rather than in opnsense/core. Reading it would mean guessing at a contract nobody has, so this
// implementation reports why instead. A 404 from its presence probe says the plugin is not
// installed HERE, which is the unavailable state working rather than a fault.

exports.dhcp_lease = {
  // probe checks only for presence. It is never separable: even a present plugin is not read, so
  // it can never be the implementation opnview reads for this kind.
  probe: function (host) {
    var result = { probe: "status", state: "unavailable", outcome: "request_not_made" };
    var response = host.call("status");
    if (response.error) {
      return { result: result, error: response.error };
    }
    result.detail = "the end-of-life plugin is not installed";
    if (response.ok) {
      result.outcome = "read_not_supported";
      result.detail = "the plugin answers, but its lease endpoint answered 404 on the surveyed " +
        "firmware and its field contract is unverified, so opnview does not read it";
    } else if (response.outcome === "not_found") {
      result.outcome = "plugin_not_installed";
    } else {
      // A refusal or a failure says nothing about whether the plugin is there; the outcome names
      // what the firewall answered.
      result.outcome = response.availability_outcome;
    }
    return { result: result };
  },

  // leases refuses, with the reason.
  leases: function () {
    return {
      result: { probe: "status", state: "unavailable", outcome: "read_not_supported" },
      unsupported: "the ISC lease endpoint answered 404 on the surveyed firmware and its field contract " +
        "is unverified"
    };
  }
};
