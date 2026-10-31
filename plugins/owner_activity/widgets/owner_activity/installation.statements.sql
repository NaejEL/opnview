-- The installation's members this widget names: the installation plugin's views, read through the
-- figures library's installation helpers (ADR 0021, step 2).

-- statement: widget_installation_owners
SELECT * FROM plugin_installation.owner_name ORDER BY id;

-- statement: widget_installation_client
SELECT * FROM plugin_installation.client_name WHERE id = :client_id;
