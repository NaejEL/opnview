-- The logged connection legs: every filter-log record, one row each, as the core stored it (PA3 of the
-- plugin-API amendment to specs/SPEC-backend-completion-plugins.md). A LEG is one record of a connection
-- crossing the firewall (docs/data-model.md, *Vocabulary*): pf logs the packet that creates a state on every
-- interface a logging rule creates one on, so a connection is a first leg, the `in` record on the interface
-- it arrived on, and a second leg, the `out` record on the one it left by, paired by the core
-- (pair_outcome, paired_flow_id); an unpaired record is not_paired, and a record the pairing has not
-- examined yet has none. Unlike the traffic dataset, the second leg is here: a derivation joining another
-- source's records to the logged connections reads both legs.
--
-- device is the network device the record names, pf's `interface` field; interface is the OPNsense
-- interface discovery assigned that device to, or null; direction is pf's own `in`/`out` per interface;
-- the tuple is the one pf logged, as the leg saw it; src_is_this_firewall and dst_is_this_firewall are 1
-- when that end is an address this firewall held (*This Firewall*). flow_id is the record's identity, which
-- a set-based read keys on (host.datasets.rows, keys). canonical_src_address and canonical_dst_address are the
-- tuple's two addresses in their canonical text -- an address compared by value, never by how pf wrote it
-- (null for a text that is no address) -- which the index of the legs by device and canonical addresses
-- serves; ingested_at is when the core stored the leg, so a derivation reads the legs stored since its last
-- run.
SELECT f.observed_at,
       f.id AS flow_id,
       f.interface_device AS device,
       i.identifier AS interface,
       f.direction,
       f.action,
       f.pair_outcome,
       f.paired_flow_id,
       f.src_address,
       f.dst_address,
       canonical_address(f.src_address) AS canonical_src_address,
       canonical_address(f.dst_address) AS canonical_dst_address,
       f.src_port,
       f.dst_port,
       f.protocol,
       f.ip_version,
       CASE WHEN f.src_is_this_firewall THEN 1 ELSE 0 END AS src_is_this_firewall,
       CASE WHEN f.dst_is_this_firewall THEN 1 ELSE 0 END AS dst_is_this_firewall,
       f.packet_bytes,
       f.ingested_at
FROM flow AS f
LEFT JOIN interface AS i ON i.device = f.interface_device
WHERE f.observed_at >= :from
  AND f.observed_at < :to
