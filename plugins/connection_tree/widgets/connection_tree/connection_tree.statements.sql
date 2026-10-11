-- opnview — the read statements the connection_tree widget's script runs.
--
-- Same conventions as every *.statements.sql file of the tree: each statement begins with its
-- marker line and binds its parameters by name. sql/schema-checks.sh plans each statement at both
-- seed sizes. Every column is named with AS, because the script reads a row as an object of its
-- columns. The tree's rows themselves are the core's (host.flows.destinations).


-- An operator's name, from any geolocated address carrying its AS number.
-- statement: widget_operator_name
SELECT operator AS operator FROM geo_asn WHERE asn = :asn AND operator IS NOT NULL LIMIT 1;
