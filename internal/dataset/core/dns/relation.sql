-- The resolver's lookups, one row each. A lookup the resolver blocked or dropped counts as blocked,
-- as widget_dns_buckets counts it. The domain is a domain name, so a query touching it is withheld
-- under aggregate_mode = no_domains.
SELECT r.looked_up_at,
       r.domain,
       r.resolver,
       r.action,
       r.rcode,
       r.client_id,
       i.identifier AS interface,
       1 AS lookups,
       CASE WHEN r.action IN ('block', 'drop') THEN 1 ELSE 0 END AS blocked
FROM dns_resolution AS r
LEFT JOIN interface AS i ON i.id = r.interface_id
WHERE r.looked_up_at >= :from
  AND r.looked_up_at < :to
