package contract

import (
	"errors"
	"regexp"
	"slices"

	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// A manifest's tables as the store creates them. The conversion lives here, with the manifest, so
// that the loader and the store's test template build the same tables from the same declaration
// (ADR 0020: the built-ins' tables exist in every test database as they exist in a started service).

// DeclaredTables checks every table a manifest declares and returns them as the store creates them.
func DeclaredTables(manifest Manifest) ([]plugindecl.PluginTable, *Refusal) {
	var tables []plugindecl.PluginTable
	seen := map[string]bool{}
	for _, declaration := range manifest.Tables {
		if seen[declaration.Name] {
			return nil, Refuse(manifest.Name, RefusalTableInvalid, "the table %q is declared twice", declaration.Name)
		}
		seen[declaration.Name] = true
		table := plugindecl.PluginTable{Plugin: manifest.Name, Table: declaration.Name,
			PrimaryKey: declaration.PrimaryKey, Counters: declaration.Counters}
		for _, column := range declaration.Columns {
			converted := plugindecl.PluginColumn{Name: column.Name, Type: column.Type, Nullable: column.Nullable,
				CarriesDomain: column.CarriesDomain, Role: column.Role, Unit: column.Unit, Identity: column.Identity,
				Generated: column.Generated, Default: column.Default}
			if column.Default != "" {
				if err := sqldb.CheckExpression(column.Default); err != nil {
					return nil, Refuse(manifest.Name, RefusalTableInvalid, "the default of the column %q of %s: %v",
						column.Name, declaration.Name, err)
				}
			}
			if column.Generated != "" {
				if err := sqldb.CheckExpression(column.Generated); err != nil {
					return nil, Refuse(manifest.Name, RefusalTableInvalid, "the generated column %q of %s: %v",
						column.Name, declaration.Name, err)
				}
			}
			if column.References != "" {
				referencedTable, referencedColumn, refusal := referencedBy(manifest, declaration.Name, column)
				if refusal != nil {
					return nil, refusal
				}
				converted.ReferencesTable, converted.ReferencesColumn = referencedTable, referencedColumn
			}
			table.Columns = append(table.Columns, converted)
		}
		for _, index := range declaration.Indexes {
			for _, entry := range index.Columns {
				if plugindecl.IsIndexExpression(entry) {
					if err := sqldb.CheckExpression(entry); err != nil {
						return nil, Refuse(manifest.Name, RefusalTableInvalid, "an index of %s: %v", declaration.Name, err)
					}
				}
			}
			if index.Where != "" {
				if err := sqldb.CheckExpression(index.Where); err != nil {
					return nil, Refuse(manifest.Name, RefusalTableInvalid, "an index of %s: %v", declaration.Name, err)
				}
			}
			table.Indexes = append(table.Indexes, plugindecl.PluginIndex{Columns: index.Columns, Unique: index.Unique,
				Where: index.Where, NullsNotDistinct: index.NullsNotDistinct})
		}
		for _, check := range declaration.Checks {
			if err := sqldb.CheckExpression(check.Expression); err != nil {
				return nil, Refuse(manifest.Name, RefusalTableInvalid, "the check %q of %s: %v", check.Name,
					declaration.Name, err)
			}
			table.Checks = append(table.Checks, plugindecl.PluginCheck{Name: check.Name, Expression: check.Expression})
		}
		grantees := map[string]bool{}
		for _, grant := range declaration.WritableBy {
			if grant.Kind != "" {
				if grant.Plugin != "" || len(grant.Columns) > 0 || !NamePattern.MatchString(grant.Kind) || grantees["kind:"+grant.Kind] {
					return nil, Refuse(manifest.Name, RefusalTableInvalid, "the table %q is writable by the providers of "+
						"%q, which is not a kind named once, with no plugin and no columns beside it", declaration.Name, grant.Kind)
				}
				grantees["kind:"+grant.Kind] = true
				table.WritableBy = append(table.WritableBy, plugindecl.WriteGrant{Kind: grant.Kind})
				continue
			}
			if !NamePattern.MatchString(grant.Plugin) || grant.Plugin == manifest.Name || grantees[grant.Plugin] {
				return nil, Refuse(manifest.Name, RefusalTableInvalid, "the table %q is writable by %q, which "+
					"is not another plugin's name named once", declaration.Name, grant.Plugin)
			}
			grantees[grant.Plugin] = true
			for _, column := range grant.Columns {
				if !slices.ContainsFunc(declaration.Columns, func(c ColumnDeclaration) bool { return c.Name == column }) {
					return nil, Refuse(manifest.Name, RefusalTableInvalid, "the table %q grants %s the column %q, "+
						"which it does not declare", declaration.Name, grant.Plugin, column)
				}
			}
			table.WritableBy = append(table.WritableBy, plugindecl.WriteGrant{Plugin: grant.Plugin,
				Columns: append([]string(nil), grant.Columns...)})
		}
		if declaration.Retention != nil {
			table.TimeColumn = declaration.Retention.Column
			table.RetentionSeconds = declaration.Retention.Seconds
			if setting := declaration.Retention.Setting; setting != "" {
				if !declaresIntegerSetting(manifest, setting) {
					return nil, Refuse(manifest.Name, RefusalTableInvalid, "the table %q keeps its rows as long "+
						"as the setting %q says, which the manifest does not declare as an integer setting", declaration.Name, setting)
				}
				table.RetentionSetting = manifest.Name + "_" + setting
			}
		}
		if err := plugindecl.ValidatePluginTable(table); err != nil {
			if errors.Is(err, plugindecl.ErrUniqueKeyWithoutTime) {
				return nil, Refuse(manifest.Name, RefusalUniqueKeyWithoutTime, "%v", err)
			}
			return nil, Refuse(manifest.Name, RefusalTableInvalid, "%v", err)
		}
		tables = append(tables, table)
	}
	return tables, nil
}

// referencePattern is what a column's references matches: table(column), or plugin.table(column).
var referencePattern = regexp.MustCompile(`^(?:([a-z][a-z0-9_]*)\.)?([a-z][a-z0-9_]*)\(([a-z][a-z0-9_]*)\)$`)

// referencedBy resolves a column's references to the qualified table and the column it names: a table of
// the plugin's own, or of a plugin the manifest requires, which loads first (ADR 0019).
func referencedBy(manifest Manifest, tableName string, column ColumnDeclaration) (string, string, *Refusal) {
	match := referencePattern.FindStringSubmatch(column.References)
	if match == nil {
		return "", "", Refuse(manifest.Name, RefusalTableInvalid, "the column %q of %s references %q, which is "+
			"not table(column) or plugin.table(column)", column.Name, tableName, column.References)
	}
	owner, referencedTable, referencedColumn := match[1], match[2], match[3]
	if owner == "" || owner == manifest.Name {
		owner = manifest.Name
		found := false
		for _, declared := range manifest.Tables {
			if declared.Name == referencedTable {
				found = true
			}
		}
		if !found {
			return "", "", Refuse(manifest.Name, RefusalTableInvalid, "the column %q of %s references the table "+
				"%q, which the plugin does not declare", column.Name, tableName, referencedTable)
		}
	} else if !slices.Contains(manifest.Requires, owner) {
		return "", "", Refuse(manifest.Name, RefusalTableInvalid, "the column %q of %s references a table of "+
			"the plugin %s, which the manifest does not require", column.Name, tableName, owner)
	}
	return plugindecl.QuoteIdentifier(plugindecl.PluginSchema(owner)) + "." + plugindecl.QuoteIdentifier(referencedTable),
		referencedColumn, nil
}

// declaresIntegerSetting reports whether a manifest declares an integer setting of a key.
func declaresIntegerSetting(manifest Manifest, key string) bool {
	for _, setting := range manifest.Settings {
		if setting.Key == key && setting.Type == "integer" {
			return true
		}
	}
	return false
}
