package settingfields

import (
	"context"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
)

// overlayReader is the stored settings with some rows replaced by what was sent: what
// config.Load would read if the save went through.
type overlayReader struct {
	base   config.SettingReader
	values map[string]string
}

// Setting reads a sent value first, then the stored row.
func (o overlayReader) Setting(ctx context.Context, key string) (string, bool, error) {
	if value, sent := o.values[key]; sent {
		return value, true, nil
	}
	return o.base.Setting(ctx, key)
}

// AcceptedByLoad says whether config.Load accepts the stored configuration with the sent rows
// put over it. It is the validation every settings save runs, on every surface that saves a
// setting: what Load refuses is refused, nothing is stored, and what Load accepts is written.
func AcceptedByLoad(ctx context.Context, base config.SettingReader, values map[string]string) bool {
	_, err := config.Load(ctx, overlayReader{base: base, values: values})
	return err == nil
}

// TypedIntervalSeconds reads an interval in whole seconds. An interval is a positive whole
// number; the ceiling is config.MaxIntervalSeconds, the largest whole number of seconds a
// time.Duration can hold, which is also the bound Load applies to a stored row, so a figure
// accepted here is one Load will read back. A figure above it is refused on its own key rather
// than as "not a number", because it IS a number.
func TypedIntervalSeconds(typed string) (int64, web.MessageKey) {
	seconds, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	if err != nil || seconds <= 0 {
		return 0, messages.MsgCollectionValueInvalid
	}
	if seconds > config.MaxIntervalSeconds {
		return 0, messages.MsgCollectionIntervalTooLarge
	}
	return seconds, ""
}
