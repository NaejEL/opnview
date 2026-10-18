test("each message with an event is a row, the others are skipped", function (t) {
  t.deliverSyslog([
    "<14>1 2026-09-27T10:00:00+00:00 example-host example-daemon - - - event=sync bytes=4096",
    "<14>1 2026-09-27T10:00:01+00:00 example-host example-daemon - - - nothing to see"
  ]);
  var rows = t.pushed("events");
  t.equal(rows.length, 1, "rows");
  t.equal(rows[0].event, "sync", "the event");
  t.equal(rows[0].bytes, 4096, "the bytes");
  t.equal(t.pushCounters("events").skipped, 1, "skipped messages");
  t.equal(t.pushCounters("events").script_calls, 1, "one call for the batch");
});
