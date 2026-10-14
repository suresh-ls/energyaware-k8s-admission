package signal

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC)

// valid returns the example signal from design doc 4.5.6.
func valid() DemandSignal {
	return DemandSignal{
		SignalType:  SignalTypeDemand,
		Provider:    "kepler",
		Value:       7.5,
		Unit:        UnitKilowatt,
		Cap:         10.0,
		Utilization: 0.75,
		Status:      StatusWithinCap,
		Timestamp:   t0,
		ValidUntil:  t0.Add(time.Hour),
		Quality:     QualityFresh,
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*DemandSignal)
		wantErr string // substring; empty means valid
	}{
		{"design doc example", func(s *DemandSignal) {}, ""},
		{"exactly at cap is within cap", func(s *DemandSignal) {
			s.Value, s.Utilization = 10, 1
		}, ""},
		{"over cap", func(s *DemandSignal) {
			s.Value, s.Utilization, s.Status = 12, 1.2, StatusExceeded
		}, ""},
		{"zero demand", func(s *DemandSignal) {
			s.Value, s.Utilization = 0, 0
		}, ""},
		{"tiny fake-meter value in watts", func(s *DemandSignal) {
			s.Unit, s.Value, s.Cap, s.Utilization = UnitWatt, 8e-5, 1e-4, 0.8
		}, ""},
		{"stale quality still structurally valid", func(s *DemandSignal) {
			s.Quality = QualityStale
		}, ""},

		{"wrong signal type", func(s *DemandSignal) { s.SignalType = "carbon" }, "signal_type"},
		{"missing provider", func(s *DemandSignal) { s.Provider = "" }, "provider"},
		{"unsupported unit", func(s *DemandSignal) { s.Unit = "MW" }, "unit"},
		{"negative value", func(s *DemandSignal) { s.Value = -1 }, "value"},
		{"NaN value", func(s *DemandSignal) { s.Value = math.NaN() }, "value"},
		{"Inf value", func(s *DemandSignal) { s.Value = math.Inf(1) }, "value"},
		{"zero cap", func(s *DemandSignal) { s.Cap = 0 }, "cap"},
		{"negative cap", func(s *DemandSignal) { s.Cap = -5 }, "cap"},
		{"zero timestamp", func(s *DemandSignal) { s.Timestamp = time.Time{} }, "timestamp"},
		{"missing valid_until", func(s *DemandSignal) { s.ValidUntil = time.Time{} }, "valid_until"},
		{"valid_until before timestamp", func(s *DemandSignal) {
			s.ValidUntil = t0.Add(-time.Minute)
		}, "valid_until"},
		{"invalid quality", func(s *DemandSignal) { s.Quality = "OLD" }, "quality"},
		{"invalid status", func(s *DemandSignal) { s.Status = "MAYBE" }, "status"},
		{"utilization mismatch", func(s *DemandSignal) { s.Utilization = 0.5 }, "utilization"},
		{"status says within cap but value exceeds", func(s *DemandSignal) {
			s.Value, s.Utilization = 12, 1.2
		}, "inconsistent"},
		{"status says exceeded but value within cap", func(s *DemandSignal) {
			s.Status = StatusExceeded
		}, "inconsistent"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := valid()
			tc.mutate(&s)
			err := s.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err)
			}
		})
	}
}

func TestIsUsable(t *testing.T) {
	for q, want := range map[Quality]bool{
		QualityFresh: true, QualityCached: true, QualityStale: false,
	} {
		s := valid()
		s.Quality = q
		if got := s.IsUsable(); got != want {
			t.Errorf("quality %s: IsUsable = %v, want %v", q, got, want)
		}
	}
}

func TestIsExceeded(t *testing.T) {
	s := valid()
	if s.IsExceeded() {
		t.Error("within-cap signal reported as exceeded")
	}
	s.Status = StatusExceeded
	if !s.IsExceeded() {
		t.Error("exceeded signal not reported as exceeded")
	}
}

func TestJSONFieldNames(t *testing.T) {
	b, err := json.Marshal(valid())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"signal_type", "provider", "value", "unit", "cap",
		"utilization", "status", "timestamp", "valid_until", "quality"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing JSON field %q", k)
		}
	}
	if m["status"] != "WITHIN_CAP" || m["quality"] != "FRESH" {
		t.Errorf("unexpected enum encoding: %v %v", m["status"], m["quality"])
	}
}

func TestJSONRoundTrip(t *testing.T) {
	b, _ := json.Marshal(valid())
	var got DemandSignal
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("round-tripped signal invalid: %v", err)
	}
	if !got.Timestamp.Equal(t0) {
		t.Errorf("timestamp changed: %v", got.Timestamp)
	}
}