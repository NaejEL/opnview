-- The interfaces' own counters, one row per sample: the increase since the device's previous
-- sample of the same counter, and that increase per second. The counter names the unit (bytes,
-- packets or errors, in or out). A counter that went down was reset, and its row has neither
-- figure -- never a negative rate, never a zero -- as ReadInterfaceThroughput reads it. The
-- previous sample is looked for up to an hour before the window.
SELECT c.sampled_at,
       i.identifier AS interface,
       c.subject_key AS device,
       c.measure AS counter,
       CASE WHEN c.previous_value IS NULL OR c.value < c.previous_value THEN NULL
            ELSE c.value - c.previous_value END AS increase,
       CASE WHEN c.previous_value IS NULL OR c.value < c.previous_value OR c.sampled_at <= c.previous_at
            THEN NULL
            ELSE (c.value - c.previous_value) / (c.sampled_at - c.previous_at) END AS per_second
FROM (SELECT s.sampled_at,
             s.subject_key,
             s.measure,
             s.value,
             lag(s.value) OVER counter_order AS previous_value,
             lag(s.sampled_at) OVER counter_order AS previous_at
      FROM measurement_sample AS s
      WHERE s.subject_kind = 'interface'
        AND s.measure IN ('bytes_in', 'bytes_out', 'packets_in', 'packets_out', 'errors_in', 'errors_out')
        AND s.sampled_at >= :from - 3600
        AND s.sampled_at < :to
      WINDOW counter_order AS (PARTITION BY s.subject_key, s.measure ORDER BY s.sampled_at)) AS c
LEFT JOIN interface AS i ON i.device = c.subject_key
WHERE c.sampled_at >= :from
