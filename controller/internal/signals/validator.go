package signals

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// validateSignal checks shared signal invariants. maxAge applies to the
// observation/collection age, not to a future market delivery timestamp.
func validateSignal(signal Signal, now time.Time, maxAge time.Duration) error {
	if strings.TrimSpace(signal.SignalType) == "" {
		return fmt.Errorf("signal type is required")
	}
	if strings.TrimSpace(signal.Provider) == "" {
		return fmt.Errorf("provider is required")
	}
	if strings.TrimSpace(signal.Region) == "" {
		return fmt.Errorf("region is required")
	}
	if strings.TrimSpace(signal.Unit) == "" {
		return fmt.Errorf("unit is required")
	}
	if math.IsNaN(signal.Value) || math.IsInf(signal.Value, 0) {
		return fmt.Errorf("signal value must be finite")
	}
	if signal.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}
	if signal.ValidUntil.IsZero() {
		return fmt.Errorf("valid_until is required")
	}
	if !now.Before(signal.ValidUntil) {
		return fmt.Errorf("signal expired at %s", signal.ValidUntil.Format(time.RFC3339Nano))
	}
	// Future market delivery timestamps are valid for cost signals. For other
	// non-forecast signals, a future source timestamp is likely a data error.
	if signal.Timestamp.After(now) && normalizeSignalType(signal.SignalType) != SignalTypeCost && !signal.IsForecast {
		return fmt.Errorf("non-forecast signal timestamp is in the future")
	}
	if !signal.CollectedAt.IsZero() && signal.CollectedAt.After(now.Add(time.Minute)) {
		return fmt.Errorf("collected_at is too far in the future")
	}

	ageReference := signal.Timestamp
	if !signal.CollectedAt.IsZero() {
		ageReference = signal.CollectedAt
	}
	// Future market-period timestamps are allowed. If collection time is
	// available, use it to determine how old the retrieved data is.
	if !ageReference.After(now) && maxAge > 0 && now.Sub(ageReference) > maxAge {
		return fmt.Errorf("signal age exceeds maximum of %s", maxAge)
	}

	if normalizeSignalType(signal.SignalType) == SignalTypeDemandCap {
		if math.IsNaN(signal.Cap) || math.IsInf(signal.Cap, 0) || signal.Cap <= 0 {
			return fmt.Errorf("demand cap must be finite and positive")
		}
		if signal.Value < 0 || (signal.Unit != "W" && signal.Unit != "kW") {
			return fmt.Errorf("demand must be nonnegative power in W or kW")
		}
		ratio := signal.Value / signal.Cap
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || math.IsNaN(signal.Utilization) || math.IsInf(signal.Utilization, 0) || math.Abs(signal.Utilization-ratio) > 1e-9 {
			return fmt.Errorf("demand utilization must equal value/cap")
		}
		status := "WITHIN_CAP"
		if signal.Value > signal.Cap {
			status = "EXCEEDED"
		}
		if signal.Status != status {
			return fmt.Errorf("demand status must be %s", status)
		}
		if signal.Quality == "STALE" {
			return fmt.Errorf("stale demand is unavailable")
		}
	}
	return nil
}
