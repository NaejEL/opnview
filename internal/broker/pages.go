package broker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/infer"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// Pages and windows (A10, "Parameters").
//
// THE CORE HANDLES PAGINATION. A request carrying a page and a page size -- the grid-search set
// current / rowCount -- is one datum whatever page it asks for: the broker walks the pages from the
// first until the answer is complete, within the bound below, and serves each requester the page
// it asked for out of the whole. rowCount = -1 is every row in one call.
//
// A RECOGNISED WINDOW IS VERIFIED, NEVER TRUSTED. A record stream with a window parameter is asked
// only for the window after its newest stored row; when a returned row's cursor lies outside the
// window asked for, the parameter is recorded as not honoured and no longer relied on: the datum
// is read with the window the requester gave, and served from the mirror filtered on its cursor.

// MaxPages bounds the pages one walk reads.
const MaxPages = 1000

// MaxRows bounds the rows one walk reads.
const MaxRows = 100000

// mainRows returns the array at a row-set path ending in an array, or nil.
func mainRows(value *answer.Value, rowSet string) *answer.Value {
	if value == nil || !strings.HasSuffix(rowSet, answer.AnyItem) {
		return nil
	}
	steps := answer.Steps(rowSet)
	container := value
	for _, step := range steps[:len(steps)-1] {
		if step == answer.AnyItem || step == answer.AnyMember {
			return nil
		}
		container = container.Field(step)
		if container == nil {
			return nil
		}
	}
	if container.Kind != answer.Array {
		return nil
	}
	return container
}

// countRows is how many rows an answer's main row set holds.
func countRows(value *answer.Value, rowSet string) int {
	if rows := mainRows(value, rowSet); rows != nil {
		return len(rows.Items)
	}
	return 0
}

// sliceRows returns a copy of an answer whose main row set keeps rows [offset, offset+limit); the
// containers along the path are copied, nothing else.
func sliceRows(value *answer.Value, rowSet string, offset, limit int) *answer.Value {
	if value == nil || !strings.HasSuffix(rowSet, answer.AnyItem) || limit <= 0 {
		return value
	}
	steps := answer.Steps(rowSet)
	copyOf := func(node *answer.Value) *answer.Value {
		copied := *node
		if node.Kind == answer.Object {
			copied.Fields = make(map[string]*answer.Value, len(node.Fields))
			for key, member := range node.Fields {
				copied.Fields[key] = member
			}
			copied.Keys = append([]string(nil), node.Keys...)
		}
		return &copied
	}
	root := copyOf(value)
	container := root
	for _, step := range steps[:len(steps)-1] {
		if step == answer.AnyItem || step == answer.AnyMember {
			return value
		}
		next := container.Field(step)
		if next == nil {
			return root
		}
		copied := copyOf(next)
		container.Fields[step] = copied
		container = copied
	}
	if container.Kind != answer.Array {
		return root
	}
	items := container.Items
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	container.Items = append([]*answer.Value(nil), items[offset:end]...)
	return root
}

// setParameter sets one named parameter of a request's options, where the request carried it.
func setParameter(options *opnsense.RequestOptions, name string, value any) {
	if options.Query != nil {
		if _, present := options.Query[name]; present {
			options.Query.Set(name, fmt.Sprint(value))
			return
		}
	}
	if options.Form != nil {
		if _, present := options.Form[name]; present {
			options.Form.Set(name, fmt.Sprint(value))
			return
		}
	}
	if object, isObject := options.JSON.(map[string]any); isObject {
		if _, present := object[name]; present {
			object[name] = value
		}
	}
}

// parameterNamed finds a request's parameter of a role.
func (b *Broker) parameterNamed(request Request, options opnsense.RequestOptions, role string) (parameter, bool) {
	for _, candidate := range parametersOf(options) {
		if b.role(request.Endpoint.Path, candidate.name) == role {
			return candidate, true
		}
	}
	return parameter{}, false
}

// walkPages walks a paged datum's pages from the first, and returns the whole answer.
func (b *Broker) walkPages(ctx context.Context, datum *mirror.Datum, request Request, options opnsense.RequestOptions,
	shape infer.Shape, first *answer.Value, response opnsense.Response) (*answer.Value, int, opnsense.Response) {
	pageParameter, paged := b.parameterNamed(request, options, infer.RolePage)
	sizeParameter, sized := b.parameterNamed(request, options, infer.RolePageSize)
	if !paged || !sized || len(sizeParameter.values) == 0 {
		return first, 1, response
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeParameter.values[0]), 10, 64)
	if err != nil || size <= 0 {
		return first, 1, response
	}
	requestedPage := int64(1)
	if len(pageParameter.values) > 0 {
		requestedPage, _ = strconv.ParseInt(strings.TrimSpace(pageParameter.values[0]), 10, 64)
	}
	rows := mainRows(first, shape.RowSet)
	if rows == nil {
		return first, 1, response
	}
	if requestedPage != 1 {
		// The walk begins at the first page, whatever page was asked for: the requester is
		// served its page out of the whole.
		walkOptions := cloneOptions(options)
		setParameter(&walkOptions, pageParameter.name, typed(pageParameter, 1))
		started := b.clock.Now()
		firstResponse, err := b.client.Call(ctx, request.Endpoint, walkOptions)
		b.recordCall(ctx, datum, request.Endpoint.Path, started, len(firstResponse.Body))
		if err != nil || !firstResponse.OK() {
			return first, 1, response
		}
		parsed, parseErr := answer.Parse(firstResponse.Body)
		if parseErr != nil {
			return first, 1, response
		}
		first, response = parsed, firstResponse
		rows = mainRows(first, shape.RowSet)
		if rows == nil {
			return first, 1, response
		}
	}
	total := int64(-1)
	if field := first.Field("total"); field != nil && field.Kind == answer.Number {
		total, _ = strconv.ParseInt(field.Text, 10, 64)
	}
	pages := 1
	last := len(rows.Items)
	for page := int64(2); pages < MaxPages && len(rows.Items) < MaxRows; page++ {
		if int64(last) < size || (total >= 0 && int64(len(rows.Items)) >= total) {
			break
		}
		walkOptions := cloneOptions(options)
		setParameter(&walkOptions, pageParameter.name, typed(pageParameter, page))
		started := b.clock.Now()
		next, err := b.client.Call(ctx, request.Endpoint, walkOptions)
		b.recordCall(ctx, datum, request.Endpoint.Path, started, len(next.Body))
		if err != nil || !next.OK() {
			break
		}
		parsed, parseErr := answer.Parse(next.Body)
		if parseErr != nil {
			break
		}
		nextRows := mainRows(parsed, shape.RowSet)
		if nextRows == nil || len(nextRows.Items) == 0 {
			break
		}
		rows.Items = append(rows.Items, nextRows.Items...)
		last = len(nextRows.Items)
		pages++
	}
	if pages > 1 {
		response.Body = first.Marshal()
	}
	return first, pages, response
}

// typed is a page number in the type the request carried its page in.
func typed(original parameter, value int64) any {
	switch original.raw.(type) {
	case float64, int, int64:
		return value
	}
	return strconv.FormatInt(value, 10)
}

// narrowWindow asks a record stream only for the window after its newest stored row, when its
// window parameter is not known to be ignored. It returns the window asked for, for the
// verification, and the parameter's name.
func (b *Broker) narrowWindow(ctx context.Context, datum *mirror.Datum, request Request,
	options opnsense.RequestOptions) (*[2]float64, string) {
	start, found := b.parameterNamed(request, options, infer.RoleWindowStart)
	if !found || len(start.values) == 0 {
		return nil, ""
	}
	from, err := strconv.ParseFloat(strings.TrimSpace(start.values[0]), 64)
	if err != nil {
		return nil, ""
	}
	to := float64(b.now())
	if end, found := b.parameterNamed(request, options, infer.RoleWindowEnd); found && len(end.values) > 0 {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(end.values[0]), 64); err == nil {
			to = parsed
		}
	}
	honoured := b.mirror.Honoured(ctx, datum.EndpointID, start.name)
	if honoured != nil && !*honoured {
		// Not honoured: it is sent as the requester gave it, and never relied on.
		return nil, ""
	}
	if datum.Nature == infer.NatureRecordStream {
		if newest, found := b.mirror.NewestCursor(ctx, datum); found && newest > from {
			from = newest
			var value any = int64(newest)
			if _, isNumber := start.raw.(float64); !isNumber && start.raw == nil {
				value = strconv.FormatInt(int64(newest), 10)
			}
			setParameter(&options, start.name, value)
		}
	}
	return &[2]float64{from, to}, start.name
}

// verifyWindow records whether an answer's rows lie inside the window asked for.
func (b *Broker) verifyWindow(ctx context.Context, datum *mirror.Datum, path, name string, window [2]float64,
	shape infer.Shape, value *answer.Value, normaliser decode.Normaliser) {
	if len(shape.Cursor) == 0 {
		return
	}
	honoured := true
	for _, row := range infer.RowsAt(value, shape.RowSet) {
		cursor, ok := infer.CursorValue(row, shape.Cursor, normaliser)
		if ok && (cursor < window[0]-1 || cursor > window[1]+1) {
			honoured = false
			break
		}
	}
	if err := b.mirror.SetParameter(ctx, datum.EndpointID, name, infer.RoleWindowStart, "", &honoured); err != nil {
		b.options.Log(fmt.Sprintf("recording whether %s honours %s: %v", path, name, err))
	}
	if !honoured {
		b.options.Log(fmt.Sprintf("%s does not honour its window parameter %s: rows came back outside it; it is "+
			"no longer relied on", path, name))
	}
}
