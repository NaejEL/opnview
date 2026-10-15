// Package plugintables is the part of the store holding the tables plugins own: ensuring each on
// the database, setting an incompatible one aside, writing a plugin's pass, reading its rows and
// its state, and purging each by its declared retention. internal/store composes it into Store.
package plugintables
