package widget

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store"
)

// connection_tree: where a chosen subject goes (docs/widget-catalogue.md, "Connection
// tree"). The inside side starts at the chosen root -- an interface, a client or a person --
// with the client below it; the outside side starts at the operator, or the condition the
// destination is in, with the site name below it. Every flow lands in exactly one node per
// level on each side, so a node's children sum to the node and both roots sum to the same
// total; a level truncated by limit_per_level carries an explicit remainder child. The
// rule is the store's (ReadConnectionTree) and the mockup's
// (docs/mockups/check-tree-reconciliation.js).

var connectionTree = Preset{
	Type: "connection_tree", Shape: "tree", Windowed: true,
	Providers: []string{"firewall_log", "dns_lookup", "geo_asn"}, Primary: "firewall_log",
	Params: []Param{
		periodParam(),
		{Key: "root", Kind: KindEnum, Values: []string{"interface", "client", "owner"}, Default: "interface",
			Data: true},
		{Key: "depth", Kind: KindInteger, Min: 1, Max: 2, HasMax: true, Default: float64(2), Data: true},
		interfacesParam(),
		clientsParam(),
		{Key: "owners", Kind: KindReferenceList, Reference: "owner", Data: true},
		{Key: "measure", Kind: KindEnum, Values: []string{"bytes", "connections"}, Default: "bytes", Data: true},
		{Key: "limit_per_level", Kind: KindInteger, Min: 1, Default: float64(10), Data: true},
		{Key: "sort", Kind: KindEnum, Values: []string{"volume", "connections", "name", "recency"}, Default: "volume",
			Data: true},
		{Key: "include_blocked", Kind: KindBoolean, Default: true, Data: true},
	},
	Serve: serveConnectionTree,
}

// TreeNode is one node; its children's figures sum to its own.
type TreeNode struct {
	Key         string  `json:"key"`
	Value       float64 `json:"value"`
	Bytes       Count   `json:"bytes"`
	Connections Count   `json:"connections"`
	Blocked     Count   `json:"blocked_connections"`
	LastSeenAt  *int64  `json:"last_seen_at,omitempty"`
	// Remainder marks the remainder node of a truncated level, and how many members it
	// holds.
	Remainder *int64         `json:"remainder_members,omitempty"`
	Interface *InterfaceInfo `json:"interface,omitempty"`
	Client    *ClientInfo    `json:"client,omitempty"`
	Owner     *OwnerInfo     `json:"owner,omitempty"`
	Operator  *string        `json:"operator,omitempty"`
	SiteName  *string        `json:"site_name,omitempty"`
	Children  []*TreeNode    `json:"children,omitempty"`

	newest int64
}

// TreeData is both sides.
type TreeData struct {
	Root    string      `json:"root"`
	Measure string      `json:"measure"`
	Depth   int64       `json:"depth"`
	Inside  []*TreeNode `json:"inside"`
	Outside []*TreeNode `json:"outside"`
	Total   TreeNode    `json:"total"`
}

func serveConnectionTree(r *Request) (any, bool, error) {
	if err := r.coverFlows("tree_beyond_flow_horizon"); err != nil {
		return nil, false, err
	}
	root := r.params.text("root")
	interfaces, clients, owners := r.idSet("interfaces"), r.idSet("clients"), r.idSet("owners")
	includeBlocked := r.params.flag("include_blocked")
	inside, outside := newLevels(), newLevels()
	total := &TreeNode{Key: "total"}
	hasRows := false
	err := r.db.WidgetRows(r.ctx, "widget_connection_tree", store.Period{}, r.windowParameters(),
		func(row store.RowScanner) error {
			var (
				interfaceID, clientID, ownerID, asn               store.NullInt64
				direction                                         string
				lookupState, site                                 store.NullString
				connections, bytes, blocked, blockedBytes, newest int64
			)
			if err := row.Scan(&interfaceID, &clientID, &ownerID, &direction, &lookupState, &asn, &site,
				&connections, &bytes, &blocked, &blockedBytes, &newest); err != nil {
				return err
			}
			if (interfaces != nil && (!interfaceID.Valid || !interfaces[interfaceID.Int64])) ||
				(clients != nil && (!clientID.Valid || !clients[clientID.Int64])) ||
				(owners != nil && (!ownerID.Valid || !owners[ownerID.Int64])) {
				return nil
			}
			if !includeBlocked {
				connections -= blocked
				bytes -= blockedBytes
				blocked = 0
				if connections == 0 {
					return nil
				}
			}
			hasRows = true
			firewall := direction == directionToThisFirewall || direction == directionFromThisFirewall
			firewallInside := firewall && (lookupState.Valid || !interfaceID.Valid)
			firewallOutside := firewall && !lookupState.Valid
			clientNode := treeKey("client", clientID, "none")
			var first, second string
			switch {
			case firewallInside:
				first = store.TreeNodeThisFirewall
				if root != "client" {
					second = store.TreeNodeThisFirewall
				}
			case root == "interface":
				first, second = treeKey("interface", interfaceID, "none"), clientNode
			case root == "client":
				first = clientNode
			default:
				first = treeKey("owner", ownerID, "unassigned")
				if !clientID.Valid {
					first = "owner:none"
				}
				second = clientNode
			}
			inside.add(first, second, bytes, connections, blocked, newest)
			var destination string
			switch {
			case firewallOutside:
				destination = store.TreeNodeThisFirewall
			case direction == directionInterInterface:
				destination = directionInterInterface
			case lookupState.Valid && lookupState.String == "resolved":
				destination = treeKey("operator", asn, "none")
			case lookupState.Valid:
				destination = "unplaced:" + lookupState.String
			default:
				destination = "unplaced:no_row"
			}
			siteNode := "site:none"
			if site.Valid {
				siteNode = "site:" + site.String
			}
			outside.add(destination, siteNode, bytes, connections, blocked, newest)
			total.Bytes.Value += bytes
			total.Connections.Value += connections
			total.Blocked.Value += blocked
			return nil
		})
	if err != nil {
		return nil, false, err
	}
	limit := int(r.params.integer("limit_per_level"))
	depth := r.params.integer("depth")
	data := TreeData{Root: root, Measure: r.params.text("measure"), Depth: depth}
	data.Inside, err = r.finishLevel(inside, limit, depth, false)
	if err != nil {
		return nil, false, err
	}
	data.Outside, err = r.finishLevel(outside, limit, depth, r.noDomains())
	if err != nil {
		return nil, false, err
	}
	if r.noDomains() {
		r.withhold("outside[].children")
	}
	setCounts(total)
	total.Value = nodeValue(r.params.text("measure"), total)
	data.Total = *total
	return data, hasRows, nil
}

// treeKey renders a nullable id as a node key.
func treeKey(prefix string, value store.NullInt64, fallback string) string {
	if !value.Valid {
		return prefix + ":" + fallback
	}
	return fmt.Sprintf("%s:%d", prefix, value.Int64)
}

// levels accumulates a two-level side.
type levels struct {
	nodes    map[string]*TreeNode
	children map[string]map[string]*TreeNode
}

func newLevels() *levels {
	return &levels{nodes: map[string]*TreeNode{}, children: map[string]map[string]*TreeNode{}}
}

func (l *levels) add(first, second string, bytes, connections, blocked, newest int64) {
	node := l.nodes[first]
	if node == nil {
		node = &TreeNode{Key: first}
		l.nodes[first] = node
		l.children[first] = map[string]*TreeNode{}
	}
	accumulate(node, bytes, connections, blocked, newest)
	if second == "" {
		return
	}
	child := l.children[first][second]
	if child == nil {
		child = &TreeNode{Key: second}
		l.children[first][second] = child
	}
	accumulate(child, bytes, connections, blocked, newest)
}

func accumulate(node *TreeNode, bytes, connections, blocked, newest int64) {
	node.Bytes.Value += bytes
	node.Connections.Value += connections
	node.Blocked.Value += blocked
	node.newest = max(node.newest, newest)
}

// setCounts stamps a node's counts with their measure.
func setCounts(node *TreeNode) {
	node.Bytes.Measure, node.Bytes.LowerBound = MeasureLogged, true
	node.Connections.Measure, node.Connections.LowerBound = MeasureLogged, true
	node.Blocked.Measure, node.Blocked.LowerBound = MeasureLogged, true
	if node.newest > 0 {
		newest := node.newest
		node.LastSeenAt = &newest
	}
}

// nodeValue is the figure a node is drawn by.
func nodeValue(measure string, node *TreeNode) float64 {
	if measure == "connections" {
		return float64(node.Connections.Value)
	}
	return float64(node.Bytes.Value)
}

// finishLevel sorts, truncates and names one side. A withheld second level keeps its
// figures in the first and drops the names.
func (r *Request) finishLevel(side *levels, limit int, depth int64, withholdChildren bool) ([]*TreeNode, error) {
	var top []*TreeNode
	for key, node := range side.nodes {
		if depth >= 2 && !withholdChildren {
			var children []*TreeNode
			for _, child := range side.children[key] {
				children = append(children, child)
			}
			node.Children = r.truncate(children, limit)
		}
		top = append(top, node)
	}
	nodes := r.truncate(top, limit)
	for _, node := range nodes {
		if err := r.labelNode(node); err != nil {
			return nil, err
		}
		for _, child := range node.Children {
			if err := r.labelNode(child); err != nil {
				return nil, err
			}
		}
	}
	if nodes == nil {
		nodes = []*TreeNode{}
	}
	return nodes, nil
}

// truncate sorts a level and folds every member past limit into one remainder node, so
// the level still sums to its parent.
func (r *Request) truncate(nodes []*TreeNode, limit int) []*TreeNode {
	measure, order := r.params.text("measure"), r.params.text("sort")
	for _, node := range nodes {
		setCounts(node)
		node.Value = nodeValue(measure, node)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		switch order {
		case "connections":
			if a.Connections.Value != b.Connections.Value {
				return a.Connections.Value > b.Connections.Value
			}
		case "name":
			return a.Key < b.Key
		case "recency":
			if a.newest != b.newest {
				return a.newest > b.newest
			}
		default:
			if a.Value != b.Value {
				return a.Value > b.Value
			}
		}
		return a.Key < b.Key
	})
	if len(nodes) <= limit {
		return nodes
	}
	remainder := &TreeNode{Key: "remainder"}
	members := int64(len(nodes) - limit)
	remainder.Remainder = &members
	for _, node := range nodes[limit:] {
		accumulate(remainder, node.Bytes.Value, node.Connections.Value, node.Blocked.Value, node.newest)
	}
	setCounts(remainder)
	remainder.Value = nodeValue(measure, remainder)
	return append(nodes[:limit:limit], remainder)
}

// labelNode attaches what a node's key names.
func (r *Request) labelNode(node *TreeNode) error {
	prefix, value, found := strings.Cut(node.Key, ":")
	if !found {
		return nil
	}
	id, numeric := strconv.ParseInt(value, 10, 64)
	switch {
	case prefix == "interface" && numeric == nil:
		cache, err := r.install()
		if err != nil {
			return err
		}
		if info, known := cache.byID[id]; known {
			copied := info
			node.Interface = &copied
		}
	case prefix == "client" && numeric == nil:
		info, err := r.client(id)
		if err != nil {
			return err
		}
		node.Client = info
	case prefix == "owner" && numeric == nil:
		owners, err := r.owners()
		if err != nil {
			return err
		}
		for _, owner := range owners {
			if owner.ID == id {
				copied := owner
				node.Owner = &copied
			}
		}
	case prefix == "operator" && numeric == nil:
		var name string
		found, err := r.db.WidgetRow(r.ctx, "widget_operator_name", map[string]any{"asn": id}, &name)
		if err != nil {
			return err
		}
		if found {
			node.Operator = &name
		}
	case prefix == "site" && value != "none":
		node.SiteName = &value
	}
	return nil
}
