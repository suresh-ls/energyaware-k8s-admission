// Package signal defines the canonical signal types used by the Generic
// Signal Handler (design doc 4.5.2) and the EnergyGate Decision Engine.
package signal

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// SignalTypeDemand is the signal_type value for the demand capping signal.
const SignalTypeDemand = "demand"

// Quality describes how trustworthy a signal value is (design doc 4.5.3).
type Quality string

const (
	// QualityFresh: value was just retrieved from the provider.
	QualityFresh Quality = "FRESH"
	// QualityCached: provider failed, cached value is still within max age.
	QualityCached Quality = "CACHED"
	// QualityStale: cached value is older than max age. Must not be treated
	// as a fresh measurement; the fallback policy applies.
	QualityStale Quality = "STALE"
)

// DemandStatus is the result of comparing measured demand to the cap
// (design doc 4.5.6).
type DemandStatus string

const (
	StatusWithinCap DemandStatus = "WITHIN_CAP"
	StatusExceeded  DemandStatus = "EXCEEDED"
)

// Supported power units for the demand signal.
const (
	UnitWatt     = "W"
	UnitKilowatt = "kW"
)

// DemandSignal is the canonical representation of the demand capping signal.
// Value and Cap are expressed in Unit.
type DemandSignal struct {
	SignalType  string       `json:"signal_type"`
	Provider    string       `json:"provider"`
	Value       float64      `json:"value"`
	Unit        string       `json:"unit"`
	Cap         float64      `json:"cap"`
	Utilization float64      `json:"utilization"`
	Status      DemandStatus `json:"status"`
	Timestamp   time.Time    `json:"timestamp"`
	ValidUntil  time.Time    `json:"valid_until"`
	Quality     Quality      `json:"quality"`
}

// utilizationTolerance allows for floating point rounding when checking that
// Utilization equals Value/Cap.
const utilizationTolerance = 1e-9

// Validate checks the signal against the rules in design doc 4.5.4:
// required fields, acceptable numeric range, valid timestamps and a
// supported unit. It also checks that the derived fields (Utilization and
// Status) are consistent with Value and Cap, so a malformed signal cannot
// reach the Decision Engine.
func (s DemandSignal) Validate() error {
	if s.SignalType != SignalTypeDemand {
		return fmt.Errorf("signal_type must be %q, got %q", SignalTypeDemand, s.SignalType)
	}
	if s.Provider == "" {
		return errors.New("provider is required")
	}
	if s.Unit != UnitWatt && s.Unit != UnitKilowatt {
		return fmt.Errorf("unsupported unit %q (want %q or %q)", s.Unit, UnitWatt, UnitKilowatt)
	}
	if isBad(s.Value) || s.Value < 0 {
		return fmt.Errorf("value must be a finite number >= 0, got %v", s.Value)
	}
	if isBad(s.Cap) || s.Cap <= 0 {
		return fmt.Errorf("cap must be a finite number > 0, got %v", s.Cap)
	}
	if s.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	if s.ValidUntil.IsZero() {
		return errors.New("valid_until is required")
	}
	if !s.ValidUntil.After(s.Timestamp) {
		return errors.New("valid_until must be after timestamp")
	}
	switch s.Quality {
	case QualityFresh, QualityCached, QualityStale:
	default:
		return fmt.Errorf("invalid quality %q", s.Quality)
	}
	switch s.Status {
	case StatusWithinCap, StatusExceeded:
	default:
		return fmt.Errorf("invalid status %q", s.Status)
	}
	if math.Abs(s.Utilization-s.Value/s.Cap) > utilizationTolerance {
		return fmt.Errorf("utilization %v does not match value/cap %v", s.Utilization, s.Value/s.Cap)
	}
	wantExceeded := s.Value > s.Cap
	if wantExceeded != (s.Status == StatusExceeded) {
		return fmt.Errorf("status %q is inconsistent with value %v and cap %v", s.Status, s.Value, s.Cap)
	}
	return nil
}

// IsExceeded reports whether demand is above the configured cap.
func (s DemandSignal) IsExceeded() bool { return s.Status == StatusExceeded }

// IsUsable reports whether the signal may be treated as a current
// measurement. STALE signals are not usable; the caller must apply the
// configured fallback policy instead (design doc 4.5.3).
func (s DemandSignal) IsUsable() bool { return s.Quality != QualityStale }

func isBad(f float64) bool { return math.IsNaN(f) || math.IsInf(f, 0) }