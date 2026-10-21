package signal

import (
	"errors"
	"math"
	"testing"
)

func TestEvaluateDemand(t *testing.T) {
	tests := []struct {
		name       string
		demand     float64
		cap        float64
		wantStatus DemandStatus
		wantUtil   float64
	}{
		{"design doc example", 7.5, 10, StatusWithinCap, 0.75},
		{"well under cap", 1, 10, StatusWithinCap, 0.1},
		{"exactly at cap is within cap", 10, 10, StatusWithinCap, 1},
		{"just over cap", 10.0001, 10, StatusExceeded, 1.00001},
		{"over cap", 12, 10, StatusExceeded, 1.2},
		{"zero demand", 0, 10, StatusWithinCap, 0},
		{"tiny values (fake meter scale)", 8e-5, 1e-4, StatusWithinCap, 0.8},
		{"large values", 5e6, 1e6, StatusExceeded, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvaluateDemand(tc.demand, tc.cap)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("status = %s, want %s", got.Status, tc.wantStatus)
			}
			if math.Abs(got.Utilization-tc.wantUtil) > 1e-9 {
				t.Errorf("utilization = %v, want %v", got.Utilization, tc.wantUtil)
			}
			if got.Value != tc.demand || got.Cap != tc.cap {
				t.Errorf("value/cap not echoed: %+v", got)
			}
		})
	}
}

func TestEvaluateDemandErrors(t *testing.T) {
	tests := []struct {
		name    string
		demand  float64
		cap     float64
		wantErr error
	}{
		{"zero cap", 5, 0, ErrInvalidCap},
		{"negative cap", 5, -1, ErrInvalidCap},
		{"NaN cap", 5, math.NaN(), ErrInvalidCap},
		{"Inf cap", 5, math.Inf(1), ErrInvalidCap},
		{"negative demand", -1, 10, ErrInvalidDemand},
		{"NaN demand", math.NaN(), 10, ErrInvalidDemand},
		{"Inf demand", math.Inf(1), 10, ErrInvalidDemand},
		{"zero cap reported before bad demand", -1, 0, ErrInvalidCap},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EvaluateDemand(tc.demand, tc.cap)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// The evaluation must be consistent with DemandSignal.Validate, so a signal
// built from EvaluateDemand always passes validation.
func TestEvaluateDemandProducesValidSignal(t *testing.T) {
	for _, d := range []float64{0, 3.3, 10, 10.5, 99} {
		ev, err := EvaluateDemand(d, 10)
		if err != nil {
			t.Fatal(err)
		}
		s := valid()
		s.Value, s.Cap, s.Utilization, s.Status = ev.Value, ev.Cap, ev.Utilization, ev.Status
		if err := s.Validate(); err != nil {
			t.Errorf("demand %v: signal built from EvaluateDemand is invalid: %v", d, err)
		}
	}
}
