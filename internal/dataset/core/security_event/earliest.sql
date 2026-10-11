-- The instant of the oldest alert stored.
SELECT min(occurred_at) FROM security_event
