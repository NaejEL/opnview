// Counts the window's destinations the geolocation placed and those it could not.
exports.widget = {
  serve: function (host) {
    var window = host.request.window;
    var placed = 0;
    var unplaced = 0;
    host.query.rows("widget_example_peer_places", { from: window.from, to: window.to }).forEach(function (row) {
      if (host.peers.placement(host.services.geolocation(row.peer_address)) === "") {
        placed++;
      } else {
        unplaced++;
      }
    });
    return { data: { placed: placed, unplaced: unplaced }, has_rows: placed + unplaced > 0 };
  }
};
