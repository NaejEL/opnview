-- The start of the oldest hour holding an unmatched record.
SELECT min(period_start_at) FROM unmatched_record_aggregate_1h
