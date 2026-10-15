// Package schedule decides when each mirrored datum is refreshed and in what order calls leave
// for the firewall (A12 of the broker amendment to specs/SPEC-backend-completion-plugins.md).
//
// DEMAND DRIVES IT. Every request registers or renews its requester's demand for a datum at the
// requester's max age; a demand not renewed within its lapse span lapses. A datum's deadline is
// its last fetch plus the shortest max age among its live demands, never below its freshness
// floor, and the scheduler refreshes it ahead of that deadline so a reader's pass normally finds
// the mirror fresh. A datum nobody reads is not refreshed: an endpoint's cadence follows its most
// demanding reader, and stops when nobody reads it.
//
// NEAREST DEADLINE FIRST, WITH A REAL-TIME RESERVE. A datum whose shortest live max age is at or
// below the real-time threshold is real-time; the rest is background. While both classes have a
// call due, real-time calls take at least the reserved share of the calls that leave; while no
// real-time call is due, background calls take the whole budget -- the reserve is guaranteed, not
// a cap. Within a class the nearest deadline goes first, which is what ages a waiting background
// datum: its deadline only grows older while others' are renewed, so none is starved.
//
// THE LATENESS BOUND. Under sustained real-time load that saturates the calls-per-second ceiling R,
// background calls still leave at a rate of (1 - r) * R, r the reserved share. A background datum
// due at t therefore leaves no later than t + B / ((1 - r) * R), B being the number of background
// data due at once, and returns within one call's time-out after that; with the defaults (R = 5,
// r = 0.7) and one background datum due, that is within 0.67 s of its deadline plus its call.
//
// CEILINGS AND BACK-OFF. At most MaxInFlight calls are in flight and at most MaxPerSecond leave
// per second. A datum whose calls end in a server error, a transport failure or a time-out backs
// off exponentially up to BackoffMax; failures across several endpoints at once back off the whole
// queue. The first successful answer ends either. not_found and forbidden are states, retried at
// the readers' own cadence, never backed off.
package schedule
