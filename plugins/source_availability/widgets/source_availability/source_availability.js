// source_availability: every provider of the kinds the widget binds -- which is every kind -- with
// its condition, as the response's availability carries it, so what each source answers is
// visible rather than assumed (docs/widget-catalogue.md, "Source availability"); and, beside it,
// when its current state began, as opnview's own checks bound it (coverage row 39 of
// docs/widget-api.md): {after_at, by_at}, never one instant, or the named absence in `state`.

exports.widget = {
  serve: function (host) {
    var showInactive = host.params.flag("show_inactive");
    var since = host.envelope.availabilitySince();
    var providers = host.envelope.availability().filter(function (provider) {
      return host.params.has("kinds", provider.kind) && (showInactive || provider.active);
    }).map(function (provider) {
      var entry = {};
      Object.keys(provider).forEach(function (key) {
        entry[key] = provider[key];
      });
      entry.since = since[provider.kind + "/" + provider.provider_key];
      return entry;
    });
    return { data: { providers: providers }, has_rows: providers.length > 0 };
  }
};
