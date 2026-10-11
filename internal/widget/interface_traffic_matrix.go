package widget

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// interface_traffic_matrix: source interface by destination interface, each cell carrying
// the logged volume, the allowed and blocked connections and, on request, the rules that
// matched (docs/widget-catalogue.md, "Interface traffic matrix"). The right-hand column is
// north-south; this firewall has its own row and its own column; a paired connection is
// counted once, on its first leg.

var interfaceTrafficMatrix = Preset{
	Type: "interface_traffic_matrix", Shape: "matrix", Windowed: true,
	Providers: []string{"firewall_log"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "scope", Kind: KindEnum, Values: []string{scopeAll, scopeEastWest, scopeNorthSouth, scopeThisFirewall},
			Default: scopeAll, Data: true},
		{Key: "measure", Kind: KindEnum, Values: []string{"bytes", "allowed", "blocked"}, Default: "bytes",
			Data: true},
		interfacesParam(),
		{Key: "show_rules", Kind: KindBoolean, Default: true, Data: true},
	},
	Serve: serveInterfaceTrafficMatrix,
}

// The matrix's non-interface axis keys.
const (
	matrixOutside      = "outside"
	matrixNorthSouth   = "north_south"
	matrixThisFirewall = "this_firewall"
)

// MatrixAxis is one row or column.
type MatrixAxis struct {
	Key       string         `json:"key"`
	Interface *InterfaceInfo `json:"interface,omitempty"`
}

// MatrixRule is one rule that matched in a cell.
type MatrixRule struct {
	RuleID      *int64  `json:"rule_id"`
	Description *string `json:"description"`
	Rid         *string `json:"rid"`
	// State is resolved, or rule_not_found for a rid no known rule carries.
	State       string `json:"state"`
	Connections Count  `json:"connections"`
}

// MatrixCell is one pair.
type MatrixCell struct {
	Row    string        `json:"row"`
	Column string        `json:"column"`
	Value  float64       `json:"value"`
	Volume Volume        `json:"volume"`
	Rules  *[]MatrixRule `json:"rules,omitempty"`
}

// MatrixData is the matrix.
type MatrixData struct {
	Measure string       `json:"measure"`
	Rows    []MatrixAxis `json:"rows"`
	Columns []MatrixAxis `json:"columns"`
	Cells   []MatrixCell `json:"cells"`
	Total   Volume       `json:"total"`
}

// matrixPlacement is the row and column of a key.
func matrixPlacement(src, dst *int64, direction string) (string, string) {
	row := matrixOutside
	if src != nil {
		row = interfaceKey(*src)
	}
	column := matrixNorthSouth
	if dst != nil {
		column = interfaceKey(*dst)
	}
	switch direction {
	case directionToThisFirewall:
		column = matrixThisFirewall
	case directionFromThisFirewall:
		row = matrixThisFirewall
	case directionOutbound:
		column = matrixNorthSouth
	case directionInbound:
		row = matrixOutside
	}
	return row, column
}

func serveInterfaceTrafficMatrix(r *Request) (any, bool, error) {
	window, err := r.volumeWindow()
	if err != nil {
		return nil, false, err
	}
	cache, err := r.install()
	if err != nil {
		return nil, false, err
	}
	scope := r.params.text("scope")
	measure := r.params.text("measure")
	selected := r.idSet("interfaces")
	keep := func(row, column string) bool {
		if selected == nil {
			return true
		}
		for id := range selected {
			if row == interfaceKey(id) || column == interfaceKey(id) {
				return true
			}
		}
		return false
	}

	cells := map[[2]string]*store.VolumeFigures{}
	var total store.VolumeFigures
	for _, row := range window.Rows {
		if row.ExitLegDevice != nil || !scopeMatches(scope, row.VolumeKey) {
			continue
		}
		rowKey, columnKey := matrixPlacement(row.SrcInterfaceID, row.DstInterfaceID, row.TrafficDirection)
		if !keep(rowKey, columnKey) {
			continue
		}
		figures := cells[[2]string{rowKey, columnKey}]
		if figures == nil {
			figures = &store.VolumeFigures{}
			cells[[2]string{rowKey, columnKey}] = figures
		}
		addFigures(figures, row.VolumeFigures)
		addFigures(&total, row.VolumeFigures)
	}

	rules := map[[2]string][]MatrixRule{}
	if r.params.flag("show_rules") {
		if err := r.coverFlowsState("matrix_rules_beyond_flow_horizon"); err != nil {
			return nil, false, err
		}
		err := r.db.WidgetRows(r.ctx, "widget_matrix_rules", store.Period{}, r.windowParameters(),
			func(row store.RowScanner) error {
				var (
					src, dst, ruleID store.NullInt64
					direction        string
					description, rid store.NullString
					count            int64
				)
				if err := row.Scan(&src, &dst, &direction, &ruleID, &description, &rid, &count); err != nil {
					return err
				}
				rowKey, columnKey := matrixPlacement(nullableNumber(src), nullableNumber(dst), direction)
				if _, present := cells[[2]string{rowKey, columnKey}]; !present {
					return nil
				}
				rule := MatrixRule{RuleID: nullableNumber(ruleID), Description: nullableText(description),
					Rid: nullableText(rid), State: "resolved", Connections: loggedCount(count)}
				if rule.RuleID == nil {
					rule.State = "rule_not_found"
				}
				rules[[2]string{rowKey, columnKey}] = append(rules[[2]string{rowKey, columnKey}], rule)
				return nil
			})
		if err != nil {
			return nil, false, err
		}
	}

	data := MatrixData{Measure: measure, Cells: []MatrixCell{}, Total: logged(total)}
	for _, info := range cache.interfaces {
		if selected != nil && !selected[info.ID] {
			continue
		}
		copied := info
		data.Rows = append(data.Rows, MatrixAxis{Key: interfaceKey(info.ID), Interface: &copied})
		data.Columns = append(data.Columns, MatrixAxis{Key: interfaceKey(info.ID), Interface: &copied})
	}
	data.Rows = append(data.Rows, MatrixAxis{Key: matrixOutside}, MatrixAxis{Key: matrixThisFirewall})
	data.Columns = append(data.Columns, MatrixAxis{Key: matrixNorthSouth}, MatrixAxis{Key: matrixThisFirewall})
	for key, figures := range cells {
		cell := MatrixCell{Row: key[0], Column: key[1], Value: figuresValue(measure, *figures),
			Volume: logged(*figures)}
		if r.params.flag("show_rules") {
			list := rules[key]
			if list == nil {
				list = []MatrixRule{}
			}
			sort.Slice(list, func(i, j int) bool {
				return list[i].Connections.Value > list[j].Connections.Value
			})
			cell.Rules = &list
		}
		data.Cells = append(data.Cells, cell)
	}
	sort.Slice(data.Cells, func(i, j int) bool {
		if data.Cells[i].Value != data.Cells[j].Value {
			return data.Cells[i].Value > data.Cells[j].Value
		}
		if data.Cells[i].Row != data.Cells[j].Row {
			return data.Cells[i].Row < data.Cells[j].Row
		}
		return data.Cells[i].Column < data.Cells[j].Column
	})
	return data, len(data.Cells) > 0, nil
}
