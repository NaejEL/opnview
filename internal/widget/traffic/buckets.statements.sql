-- opnview — the read statements the widget code in buckets runs.
--
-- Same conventions as every *.statements.sql file of the tree (see service.statements.sql
-- in internal/widget for the parameter conventions). sql/schema-checks.sh plans each
-- statement at both seed sizes.


-- statement: widget_earliest_flow
SELECT min(observed_at) FROM flow;

-- The series in buckets and the hour slots beyond the flow horizon are each widget's own statements
-- since ADR 0021 (plugins/figures, the series area).
