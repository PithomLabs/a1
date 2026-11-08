// Package duration is the single duration-precision policy shared by
// attendance, timesheets and payroll inputs: measured elapsed time is
// exact integer microseconds, computed in Go from interval endpoints.
// PostgreSQL timestamptz resolves whole microseconds, so the integer
// is lossless. Legacy decimal displays quantize explicitly here —
// halves away from zero — and the database never implicitly rounds a
// business duration. Jurisdiction payroll rounding, when an
// independently verified rule boundary exists, lives in that rule
// package and never in this one.
package duration

import (
	"math"
	"time"
)

// MicrosOf returns the exact whole microseconds between two instants.
// Endpoints from the database carry whole microseconds, so no
// truncation occurs; sub-microsecond input truncates toward zero.
func MicrosOf(start, end time.Time) int64 {
	return end.Sub(start).Microseconds()
}

// HoursOf converts exact microseconds to hours for display.
func HoursOf(us int64) float64 {
	return float64(us) / 3600000000.0
}

// MinutesOf converts exact microseconds to minutes for display.
func MinutesOf(us int64) float64 {
	return float64(us) / 60000000.0
}

// Quantize2 rounds to two decimals, halves away from zero, for legacy
// decimal display columns. The exact microsecond total stays
// authoritative; this value is presentation only.
func Quantize2(v float64) float64 {
	return math.Round(v*100) / 100
}
