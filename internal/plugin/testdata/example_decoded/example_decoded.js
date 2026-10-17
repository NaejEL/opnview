// A test plugin whose push entries are decoded here, one message per call.

exports.push = {
  // A datagram: its first octet is the value, the rest the sender's name.
  binary_reading: function (message) {
    if (message.bytes.length < 2) {
      return [];
    }
    var sender = "";
    for (var index = 1; index < message.bytes.length; index++) {
      sender += String.fromCharCode(message.bytes[index]);
    }
    return [{ sender: sender, value: message.bytes[0] }];
  },
  // A line: "READING <sender> <value>"; "READING boom" throws, and loses its message.
  text_reading: function (message) {
    var parts = message.text.split(" ");
    if (parts[1] === "boom") {
      throw new Error("an unreadable reading");
    }
    return [{ sender: parts[1], value: parseInt(parts[2], 10) }];
  },
  // A line: "LOG <source> <destination> <bytes>", one filter-log record.
  log_line: function (message) {
    var parts = message.text.split(" ");
    return [{
      observed_at: Math.floor(message.received_at), device: "vtnet1", src_address: parts[1],
      dst_address: parts[2], protocol: "tcp", ip_version: 4, action: "pass", direction: "in",
      packet_bytes: parseInt(parts[3], 10)
    }];
  }
};

var unavailable = { probe: "log", state: "unavailable", outcome: "plugin_not_installed" };

exports.firewall_log = {
  probe: function () { return { result: unavailable }; },
  records: function () { return { result: unavailable, records: [] }; }
};
