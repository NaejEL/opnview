package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// host.buckets: the time buckets of a windowed widget's series, as the core cuts them.
//
//	host.buckets.width()                       the bucket width of the request's period, in seconds
//	host.buckets.intervals(window, width)      the buckets of a window, each clipped to it: [{from, to}]
//	host.buckets.floorMod(value, width)        the non-negative remainder of value by width
//	host.buckets.isGap(bucket, covered, gaps)  whether a bucket lies outside the covered interval (null
//	                                           for none) or meets one of the gaps

func init() {
	script.RegisterHostArea("buckets", func(call *script.ScriptCall, area *goja.Object) {
		call.Function(area, "width", func(goja.FunctionCall) goja.Value {
			return call.VM.ToValue(traffic.BucketWidth(call.Request))
		})
		call.Function(area, "intervals", func(arguments goja.FunctionCall) goja.Value {
			window := call.IntervalOf(arguments.Argument(0), "the window")
			width := call.IntegerOf(arguments.Argument(1), "the width")
			if width <= 0 {
				call.Throw("the bucket width is %d, not a positive number of seconds", width)
			}
			intervals := traffic.BucketIntervals(window, width)
			if intervals == nil {
				intervals = []envelope.Interval{}
			}
			return call.Value(intervals)
		})
		call.Function(area, "floorMod", func(arguments goja.FunctionCall) goja.Value {
			width := call.IntegerOf(arguments.Argument(1), "the width")
			if width <= 0 {
				call.Throw("the width is %d, not a positive number", width)
			}
			return call.VM.ToValue(traffic.FloorMod(call.IntegerOf(arguments.Argument(0), "the value"), width))
		})
		call.Function(area, "isGap", func(arguments goja.FunctionCall) goja.Value {
			bucket := call.IntervalOf(arguments.Argument(0), "the bucket")
			covered := call.OptionalIntervalOf(arguments.Argument(1), "the covered interval")
			var gaps []envelope.Gap
			if !script.Absent(arguments.Argument(2)) {
				call.DecodeArgument(arguments.Argument(2), "the gaps", &gaps)
			}
			return call.VM.ToValue(traffic.IsGapBucket(bucket, covered, gaps))
		})
	})
}
