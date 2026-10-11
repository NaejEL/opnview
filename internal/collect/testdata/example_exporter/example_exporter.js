// A second implementation of the flow_record kind, which exists only for the test proving that an
// exporter is added by its directory alone: it reads its capture configuration from an endpoint of its
// own, in a shape of its own, and maps its records from Information Elements in an order of its own.

function readState(host) {
  var result = { probe: "export_state", state: "unavailable", outcome: "request_not_made" };
  var response = host.call("export_state");
  if (response.error) {
    return { result: result, error: response.error };
  }
  if (!response.ok) {
    result.outcome = response.availability_outcome;
    return { result: result, failed: true };
  }
  var body = response.decodable ? response.value : null;
  if (body === null || typeof body !== "object" || !Array.isArray(body.points)) {
    result.outcome = "undecodable";
    return { result: result, failed: true };
  }
  return { result: result, body: body };
}

exports.flow_record = {
  probe: function (host) {
    var read = readState(host);
    if (read.error || read.failed) {
      return { result: read.result, error: read.error || null };
    }
    read.result.state = "reachable";
    read.result.outcome = "healthy";
    read.result.separable = true;
    return { result: read.result };
  },
  capture: function (host) {
    var read = readState(host);
    if (read.error || read.failed) {
      return { result: read.result, error: read.error || "the export state could not be read" };
    }
    read.result.state = "reachable";
    read.result.outcome = "healthy";
    var capture = {
      enabled: read.body.running === true,
      interfaces: read.body.points.map(function (point) {
        return { identifier: point.name, ingress: point.receive === true, egress: point.send === true };
      })
    };
    if (typeof read.body.active_timeout === "number") {
      capture.active_timeout_seconds = read.body.active_timeout;
    }
    return { result: read.result, capture: capture };
  }
};
