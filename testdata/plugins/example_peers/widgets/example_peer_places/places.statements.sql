-- The window's destinations, one row each: the column named peer_address is what the core places,
-- so their geolocations are read together at the first the script asks for.
-- statement: widget_example_peer_places
SELECT dst_address AS peer_address, count(*) AS connections
FROM flow
WHERE observed_at >= :from AND observed_at < :to
GROUP BY dst_address
ORDER BY dst_address;
