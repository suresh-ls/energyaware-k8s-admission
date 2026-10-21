package signal

import (
	"errors"
	"fmt"
)

// Errors returned by EvaluateDemand.
var (
	// ErrInvalidCap is returned when the configured demand cap is not a
	// positive, finite number (design doc 4.5.6: "IF demandCap <= 0 THEN
	// RETURN ERROR").
	ErrInvalidCap = errors.New("demand cap must be a finite number greater than 0")
	// ErrInvalidDemand is returned when the measured demand is negative,
	// NaN or infinite.
	ErrInvalidDemand = errors.New("current demand must be a finite number >= 0")
)

// DemandEvaluation is the result of comparing measured demand with the cap.
// It mirrors the structure returned by the pseudocode in design doc 4.5.6.
type DemandEvaluation struct {
	Value       float64
	Cap         float64
	Utilization float64
	Status      DemandStatus
}

// EvaluateDemand compares currentDemand with demandCap. Both values must be in
// the same unit; the function itself is unit-agnostic.
//
// It implements the pseudocode in design doc 4.5.6:
//
//	if demandCap <= 0           -> error
//	utilization = demand / cap
//	if demand > cap             -> EXCEEDED
//	else                        -> WITHIN_CAP
//
// A demand exactly equal to the cap is WITHIN_CAP.
func EvaluateDemand(currentDemand, demandCap float64) (DemandEvaluation, error) {
	if isBad(demandCap) || demandCap <= 0 {
		return DemandEvaluation{}, fmt.Errorf("%w: got %v", ErrInvalidCap, demandCap)
	}
	if isBad(currentDemand) || currentDemand < 0 {
		return DemandEvaluation{}, fmt.Errorf("%w: got %v", ErrInvalidDemand, currentDemand)
	}

	status := StatusWithinCap
	if currentDemand > demandCap {
		status = StatusExceeded
	}

	return DemandEvaluation{
		Value:       currentDemand,
		Cap:         demandCap,
		Utilization: currentDemand / demandCap,
		Status:      status,
	}, nil
}
