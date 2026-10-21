
package signals

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func validateSignal(signal Signal, now time.Time) error {
	if strings.TrimSpace(signal.SignalType) == "" {
		return fmt.Errorf("signal type is required")
	}

	if strings.TrimSpace(signal.Provider) == "" {
		return fmt.Errorf("signal provider is required")
	}

	if strings.TrimSpace(signal.Region) == "" {
		return fmt.Errorf("signal region is required")
	}

	if strings.TrimSpace(signal.Unit) == "" {
		return fmt.Errorf("signal unit is required")
	}

	if strings.TrimSpace(signal.Quality) == "" {
		return fmt.Errorf("signal quality is required")
	}

	if math.IsNaN(signal.Value) || math.IsInf(signal.Value, 0) {
		return fmt.Errorf("signal value must be finite")
	}

	if signal.Timestamp.IsZero() {
		return fmt.Errorf("signal timestamp is required")
	}

	if signal.ValidUntil.IsZero() {
		return fmt.Errorf("signal valid_until is required")
	}

	if !signal.ValidUntil.After(signal.Timestamp) {
		return fmt.Errorf("signal valid_until must be after timestamp")
	}

	if now.Before(signal.Timestamp) {
		return fmt.Errorf("signal timestamp is in the future")
	}

	if !now.Before(signal.ValidUntil) {
		return fmt.Errorf("signal has expired")
	}

	return nil
}
