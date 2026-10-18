exports.widget = {
  serve: function (host) {
    var n = 0;
    host.flows.destinations().forEach(function (row) { n++; host.figures.cover(row); });
    return { data: { root: "interface", n: n }, has_rows: true };
  }
};
