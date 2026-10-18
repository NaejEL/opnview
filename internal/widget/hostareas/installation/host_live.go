package installation

import (
	"time"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.live: the live mode a live figure is drawn in (docs/widget-api.md, "Live figures"). A live
// figure is read from the stored samples and records; reading it renews the live-interest lease, while
// which the plugins declaring a live pass run it at their live cadence, and it never calls the firewall.
//
//	host.live.cadence()  the live cadence in force, in seconds: the finest cadence of the live passes the
//	                     loaded plugins declare, or the measurement interval where none declares one
//	host.live.renew()    renews the live-interest lease, and answers the instant it lapses, in epoch
//	                     seconds, or null when the service holds no lease

func init() {
	script.RegisterHostArea("live", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		call.Function(area, "cadence", func(goja.FunctionCall) goja.Value {
			if r.LiveCadence != nil {
				if finest := r.LiveCadence(r.Ctx); finest > 0 {
					return call.VM.ToValue(finest)
				}
			}
			interval, err := r.Db.MeasurementIntervalSeconds(r.Ctx)
			if err != nil {
				return call.Fail(err)
			}
			return call.VM.ToValue(interval)
		})
		call.Function(area, "renew", func(goja.FunctionCall) goja.Value {
			r.LiveInterest.Renew(time.Unix(r.Now, 0))
			until, held := r.LiveInterest.Until()
			if !held {
				return goja.Null()
			}
			return call.VM.ToValue(until.Unix())
		})
	})
}
