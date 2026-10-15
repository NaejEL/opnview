// Package datasets mounts the data catalogue (P8 of specs/SPEC-backend-completion-plugins.md,
// docs/widget-api.md, "Data catalogue"). It draws no page: it answers JSON only, for the step-7
// application and for whoever builds a chart on the data.
//
//   - GET /api/datasets lists every dataset the core and every loaded plugin expose, each with its
//     columns, their roles and units, and the aggregations they admit.
//   - GET /api/datasets/{name}/query runs one query against one dataset, carried as the widget
//     API carries a widget's parameters, and answers in its envelope.
//
// No SQL text is accepted from the browser: a query names columns, aggregations, operators and
// values, and internal/dataset compiles it from the dataset's declaration.
package datasets
