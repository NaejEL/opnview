-- The installation's members this widget names: the installation plugin's views, read through the
-- figures library's installation helpers (ADR 0021, step 2).

-- statement: widget_installation_rule
SELECT * FROM plugin_installation.rule_label WHERE id = :rule_id;

-- statement: widget_installation_interfaces
SELECT * FROM plugin_installation.interface_label ORDER BY id;
