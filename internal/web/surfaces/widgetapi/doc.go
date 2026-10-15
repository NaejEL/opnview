// Package widgetapi mounts the widget API of step 5B on the server: one authenticated GET
// route per served widget type, built from the widget service's own list, and one route for
// every other type under the same prefix. It draws no page and carries no catalogue: it
// answers JSON only.
package widgetapi
