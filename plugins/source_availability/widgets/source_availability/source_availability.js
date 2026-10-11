// source_availability: every provider of the kinds the widget binds -- which is every kind -- with
// its condition, as the response's availability carries it, so what each source answers is
// visible rather than assumed (docs/widget-catalogue.md, "Source availability").

exports.widget = {
  serve: function (host) {
    var showInactive = host.params.flag("show_inactive");
    var providers = host.envelope.availability().filter(function (provider) {
      return host.params.has("kinds", provider.kind) && (showInactive || provider.active);
    });
    return { data: { providers: providers }, has_rows: providers.length > 0 };
  }
};
