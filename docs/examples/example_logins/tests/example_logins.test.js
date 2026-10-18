test("an accepted login is a row, and anything else of the program is counted", function (t) {
  t.deliverSyslog([
    "<38>1 2026-09-27T10:00:00+00:00 example-host sshd 4242 - - Accepted publickey for operator from 192.0.2.50 port 50022 ssh2",
    "<38>1 2026-09-27T10:00:01+00:00 example-host sshd 4242 - - Disconnected from user operator 192.0.2.50 port 50022"
  ]);
  var rows = t.pushed("logins");
  t.equal(rows.length, 1, "rows");
  t.equal(rows[0].user, "operator", "the user");
  t.equal(rows[0].client, "192.0.2.50", "the client");
  t.equal(rows[0].logged_at, 1790503200, "the header's instant");
  t.equal(t.pushCounters("logins").pattern_unmatched, 1, "lines the pattern does not match");
});
