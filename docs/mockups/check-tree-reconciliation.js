/* ====================================================================
 * CONNECTION-TREE CROSS-SIDE CHECK — run by hand, never part of
 * `checks`.
 *
 * HOW TO RUN IT. Open `docs/mockups/canvas-mockup.html` from disk —
 * `file://` is the page's own constraint and the harness needs nothing
 * more — open the browser console, paste this whole file, and read the
 * table it prints. If the browser automation in use refuses `file://`,
 * a plain static server on loopback is the same page from the same
 * bytes; nothing here reads the origin. this file's own header
 * and the other harnesses say the same thing in the same words.
 *
 * WHAT IT ASSERTS. The connection tree draws two mirrored sides from
 * one root. Each side already reconciles internally — a node's children
 * sum to the node, remainder children included — and that is exactly
 * what let a whole class of destination be dropped without any sum
 * going wrong: the right side simply never built a branch for the peers
 * the geolocation cache could not place, and the left side has no
 * opinion about it.
 *
 * So the assertion is across the two sides, and it is stated against
 * the subset the two sides share: the right side's level-1 total must
 * equal the NORTH-SOUTH total of the records in scope. The two sides
 * legitimately differ on the whole set, because the left side also
 * carries inter-interface traffic, which has no destination outside the
 * firewall at all. Comparing the two grand totals would fail on correct
 * code and teach everyone to ignore the check.
 *
 * It sweeps the parameter combinations the widget actually exposes —
 * root, depth, measure, limit_per_level, sort, include_blocked — and
 * reports every combination whose right side does not reconcile, or
 * whose level-1 children do not sum to their level-2 children.
 * ==================================================================== */

(function () {
  "use strict";

  function sumBy(rows, f) { var t = 0, i; for (i = 0; i < rows.length; i++) { t += f(rows[i]); } return t; }

  /* The widget renders to HTML, so the figures are read back out of the
   * markup it produced rather than out of a private variable: what is
   * checked is what a reader can see. Every node carries data-bytes,
   * data-conns and data-blocked, and its level is its x position, so the
   * side and the level are read from the class the renderer stamps. */
  function nodesFrom(html, sideClass) {
    var host = document.createElement("div");
    host.innerHTML = html;
    var out = [];
    host.querySelectorAll("rect.tree-node." + sideClass).forEach(function (el) {
      out.push({
        bytes: Number(el.getAttribute("data-bytes")),
        conns: Number(el.getAttribute("data-conns")),
        blocked: Number(el.getAttribute("data-blocked")),
        level: Number(el.getAttribute("data-level")),
        key: el.getAttribute("data-node"),
        parent: el.getAttribute("data-parent")
      });
    });
    return out;
  }

  var ROOTS = ["interface", "client", "owner"];
  var DEPTHS = [1, 2, 3];
  var MEASURES = ["bytes", "connections"];
  var CAPS = [2, 4, 6];
  var SORTS = ["volume", "connections", "name"];
  var BLOCKED = [true, false];

  var findings = [], combos = 0;

  ROOTS.forEach(function (root) {
    DEPTHS.forEach(function (depth) {
      MEASURES.forEach(function (measure) {
        CAPS.forEach(function (cap) {
          SORTS.forEach(function (sortKey) {
            BLOCKED.forEach(function (includeBlocked) {
              combos++;
              var w = {
                id: "check-tree", type: "connection_tree",
                parameters: {
                  root: root, depth: depth, measure: measure,
                  limit_per_level: cap, sort: sortKey, include_blocked: includeBlocked
                }
              };
              var res;
              try { res = RENDER.connection_tree(w); }
              catch (e) {
                findings.push({ root: root, depth: depth, cap: cap, sort: sortKey,
                                blocked: includeBlocked, what: "threw", detail: String(e) });
                return;
              }

              /* The expected right-side total, computed independently of
               * the renderer: every north-south record whose client is
               * in scope, with the same blocked filter the widget used. */
              var clientKeys = AGG.clients.map(function (c) { return c.key; });
              var rows = AGG.records.filter(function (r) {
                return clientKeys.indexOf(r.client) >= 0 && r.scope === "north_south";
              });
              if (!includeBlocked) {
                rows = rows.filter(function (r) { return r.flows - r.blocked > 0; });
              }
              var wantBytes = sumBy(rows, function (r) { return r.bytes; });
              var wantConns = sumBy(rows, function (r) {
                return includeBlocked ? r.flows : r.flows - r.blocked;
              });

              var right = nodesFrom(res.body, "tree-node-right");
              var l1 = right.filter(function (n) { return n.level === 1; });
              var gotBytes = sumBy(l1, function (n) { return n.bytes; });
              var gotConns = sumBy(l1, function (n) { return n.conns; });

              if (gotBytes !== wantBytes) {
                findings.push({ root: root, depth: depth, cap: cap, sort: sortKey, blocked: includeBlocked,
                                what: "right side loses volume",
                                detail: "drawn " + gotBytes + " of " + wantBytes +
                                        " (" + (wantBytes - gotBytes) + " bytes nowhere)" });
              }
              if (gotConns !== wantConns) {
                findings.push({ root: root, depth: depth, cap: cap, sort: sortKey, blocked: includeBlocked,
                                what: "right side loses connections",
                                detail: "drawn " + gotConns + " of " + wantConns });
              }

              /* And, still on the right side, every level-1 node that
               * was expanded must equal the sum of its own children. */
              var byParent = {};
              right.filter(function (n) { return n.level === 2; }).forEach(function (n) {
                if (!byParent[n.parent]) { byParent[n.parent] = []; }
                byParent[n.parent].push(n);
              });
              Object.keys(byParent).forEach(function (pkey) {
                var parent = l1.filter(function (n) { return n.key === pkey; })[0];
                if (!parent) {
                  findings.push({ root: root, depth: depth, cap: cap, sort: sortKey, blocked: includeBlocked,
                                  what: "orphan children", detail: pkey });
                  return;
                }
                var kids = sumBy(byParent[pkey], function (n) { return n.bytes; });
                if (kids !== parent.bytes) {
                  findings.push({ root: root, depth: depth, cap: cap, sort: sortKey, blocked: includeBlocked,
                                  what: "children do not sum to parent",
                                  detail: pkey + ": " + kids + " of " + parent.bytes });
                }
              });
            });
          });
        });
      });
    });
  });

  console.log("connection-tree cross-side check — " + combos + " parameter combinations, " +
              findings.length + " finding(s)");
  if (findings.length > 0) { console.table(findings); }
  return findings;
})();
