-- The firewall's interfaces as runtime discovery holds them (PA3 of the plugin-API amendment): one row per
-- interface OPNsense defines -- a VLAN, a physical link or a tunnel the firewall terminates -- by its
-- configuration identifier and the network device it runs on, the words of
-- /api/interfaces/overview/interfaces_info (docs/data-model.md, *Vocabulary*). link_kind is the core's
-- normalised class of link_type; is_upstream is 1 exactly when the interface reported gateways.
--
-- Discovery is no provider of a kind, so the dataset's freshness names `discovery`, which no provider row
-- carries: its condition reads state_unknown, never a provider's health it does not have.
SELECT i.identifier,
       i.device,
       i.description,
       i.link_type,
       i.link_kind,
       i.vlan_tag,
       CASE WHEN i.is_upstream THEN 1 ELSE 0 END AS is_upstream,
       i.status,
       i.enabled,
       i.first_seen_at,
       i.last_seen_at
FROM interface AS i
