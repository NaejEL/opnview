/*
 * opnview front -- starting. The last script the shell loads: every core file and every view has
 * registered itself by now, so the shell starts in the document's one container.
 */
(function (root) {
  "use strict";
  var opnview = root.opnview || (root.opnview = {});
  var document = root.document;
  if (!document || !document.getElementById) { return; }
  var start = function () {
    var container = document.getElementById("app");
    if (container) { opnview.shell.start(container); }
  };
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})(this);
