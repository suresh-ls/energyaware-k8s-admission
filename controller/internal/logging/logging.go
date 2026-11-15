package logging

import (
	"time"

	"github.com/go-logr/logr"
)

// Common structured logging fields used across EnergyGate components.
const (
	FieldComponent = "component"
	FieldOperation = "operation"
	FieldWorkload  = "workload"
	FieldSignal    = "signal_type"
	FieldProvider  = "provider"
	FieldRegion    = "region"
	FieldStatus    = "status"
	FieldDuration  = "duration_ms"
	FieldError     = "error"
)

// Component names.
const (
	ComponentSignalHandler = "signal-handler"
	ComponentDecisionEngine = "decision-engine"
)

// Status values.
const (
	StatusStarted = "started"
	StatusSuccess = "success"
	StatusFailure = "failure"
	StatusCacheHit = "cache_hit"
	StatusCacheMiss = "cache_miss"
)

// SinceMillis returns elapsed time in milliseconds.
func SinceMillis(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

// LogError writes a consistent structured error log.
func LogError(
	log logr.Logger,
	message string,
	err error,
	fields ...interface{},
) {
	base := []interface{}{
		FieldStatus, StatusFailure,
		FieldError, err.Error(),
	}

	log.Error(err, message, append(base, fields...)...)
}