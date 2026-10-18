exports.push = {
  events: function (messages) {
    return messages.map(function (message) {
      var pairs = {};
      String(message.message).split(" ").forEach(function (part) {
        var equals = part.indexOf("=");
        if (equals > 0) {
          pairs[part.slice(0, equals)] = part.slice(equals + 1);
        }
      });
      if (!pairs.event) {
        return null;
      }
      return { logged_at: message.timestamp, event: pairs.event, bytes: pairs.bytes === undefined ? null : Number(pairs.bytes) };
    });
  }
};
