package plugin

import (
	"context"

	"github.com/NaejEL/opnview/internal/widget"
)

// FinestLiveCadence is the finest live cadence in force among the live passes the loaded plugins declare,
// in whole seconds: each pass's setting, never below its lower bound nor its endpoints' freshness floor
// (LivePass.Cadence). 0 when no loaded plugin declares a live pass. It is what the widget host answers
// host.live.cadence() with (internal/widget/hostareas/installation/host_live.go).
func (l *Loaded) FinestLiveCadence(ctx context.Context, reader widget.SettingReader) int64 {
	finest := int64(0)
	for _, pass := range l.LivePasses() {
		seconds := int64(pass.Cadence(ctx, reader).Seconds())
		if finest == 0 || seconds < finest {
			finest = seconds
		}
	}
	return finest
}

// The built-in plugins' live passes are what a widget service reads its live cadence from when it is
// handed none, as their widgets are what it serves (loader.go).
func init() {
	widget.RegisterBuiltinLiveCadence(func(ctx context.Context, reader widget.SettingReader) int64 {
		return BuiltinsLoaded().FinestLiveCadence(ctx, reader)
	})
}
