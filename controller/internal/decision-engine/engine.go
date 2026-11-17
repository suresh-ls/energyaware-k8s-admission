package decisionengine

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/logging"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// SignalSource is deliberately small so the engine can use the real generic
// handler in production and a fake source in unit tests.
type SignalSource interface {
	GetSignals(ctx context.Context, region string) (signals.SignalSet, error)
	GetForecastSignals(ctx context.Context, request signals.SignalRequest, horizon time.Duration) ([]signals.Signal, error)
}

type Engine struct {
	signalSource SignalSource
	config       Config
	clock        func() time.Time
}

func NewEngine(source SignalSource, config Config) (*Engine, error) {
	if source == nil {
		return nil, fmt.Errorf("signal source is required")
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid decision-engine config: %w", err)
	}

	return &Engine{
		signalSource: source,
		config:       config,
		clock:        time.Now,
	}, nil
}

// Evaluate obtains current signals, checks hard constraints, and compares the
// current execution option with feasible forecast windows.
//
// Forecast support is optional at the provider level. If forecast retrieval is
// unavailable, the engine safely evaluates the current option instead.
func (e *Engine) Evaluate(
	ctx context.Context,
	workload WorkloadRequest,
) (Decision, error) {

	if ctx == nil {
		ctx = context.Background()
	}

	start := time.Now()

	log := logf.FromContext(ctx)

	if e == nil || e.signalSource == nil {
		err := fmt.Errorf("decision engine or signal source is nil")

		log.Error(
			err,
			"Decision engine evaluation failed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "evaluate",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, workload.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Decision{}, err
	}

	if err := validateWorkload(workload); err != nil {
		log.Error(
			err,
			"Invalid workload request",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "validate_workload",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, workload.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Decision{}, err
	}

	region := normalizeRegion(workload.Region)
	now := e.clock()

	log.Info(
		"Decision evaluation started",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "evaluate",
		logging.FieldWorkload, workload.Name,
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusStarted,
	)

	if err := ctx.Err(); err != nil {
		log.Error(
			err,
			"Decision evaluation cancelled",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "evaluate",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Decision{}, err
	}

	// Retrieve current signals.
	signalStart := time.Now()

	log.V(1).Info(
		"Requesting current signals",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "get_signals",
		logging.FieldWorkload, workload.Name,
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusStarted,
	)

	currentSignals, err := e.signalSource.GetSignals(ctx, region)
	if err != nil {
		log.Error(
			err,
			"Current signal retrieval failed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "get_signals",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(signalStart),
		)

		return Decision{}, fmt.Errorf(
			"retrieve current signals: %w",
			err,
		)
	}

	log.Info(
		"Current signals retrieved",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "get_signals",
		logging.FieldWorkload, workload.Name,
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(signalStart),
	)

	// Validate current signals.
	validationStart := time.Now()

	if err := validateSignalSet(
		currentSignals,
		region,
		now,
	); err != nil {
		log.Error(
			err,
			"Signal validation failed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "validate_signals",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(validationStart),
		)

		return Decision{}, err
	}

	log.V(1).Info(
		"Current signals validated",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "validate_signals",
		logging.FieldWorkload, workload.Name,
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(validationStart),
	)

	// Score the current execution window.
	windowStart := time.Now()

	currentWindow, err := e.makeWindow(
		workload,
		currentSignals,
		now,
		now,
		false,
	)
	if err != nil {
		log.Error(
			err,
			"Current execution window scoring failed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "score_current_window",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(windowStart),
		)

		return Decision{}, fmt.Errorf(
			"score current execution window: %w",
			err,
		)
	}

	log.V(1).Info(
		"Current execution window scored",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "score_current_window",
		logging.FieldWorkload, workload.Name,
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(windowStart),
		"score", currentWindow.Score.Total,
	)

	result := Decision{
		Action:      ActionRun,
		Reason:      "running now because no better feasible future window was established",
		Region:      region,
		Selected:    currentWindow,
		Current:     currentWindow,
		EvaluatedAt: now,
		Metadata: map[string]string{
			"forecast": "not evaluated yet",
		},
	}

	if stop, err := e.evaluateCloudSpot(ctx, workload, currentSignals, now, &result); stop || err != nil {
		return result, err
	}

	// Demand is a hard operational constraint.
	if demandCapExceeded(
		currentSignals.DemandCap.Utilization,
		currentSignals.DemandCap.Status,
	) {
		log.Info(
			"Demand cap exceeded",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "check_demand_cap",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			"decision", "WAIT",
		)

		if deadlineAllowsImmediateRun(workload, now) {
			result.Reason =
				"running as a deadline emergency despite the current demand-cap breach"

			log.Info(
				"Decision completed",
				logging.FieldComponent, logging.ComponentDecisionEngine,
				logging.FieldOperation, "evaluate",
				logging.FieldWorkload, workload.Name,
				logging.FieldRegion, region,
				logging.FieldStatus, logging.StatusSuccess,
				logging.FieldDuration, logging.SinceMillis(start),
				"decision", string(result.Action),
				"reason", result.Reason,
			)

			return result, nil
		}

		result.Action = ActionWait
		result.Reason =
			"current demand cap is exceeded; re-evaluate later"
		result.RequeueAfter = e.config.DefaultRequeueAfter

		log.Info(
			"Decision completed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "evaluate",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			logging.FieldDuration, logging.SinceMillis(start),
			"decision", string(result.Action),
			"reason", result.Reason,
		)

		return result, nil
	}

	// High-priority workload close to deadline.
	if workload.Priority >= e.config.PriorityRunThreshold &&
		workload.Deadline.Sub(now) <=
			workload.ExpectedRuntime+e.config.PriorityDeadlineBuffer {

		result.Reason =
			"high-priority workload is close to its deadline; run now"

		log.Info(
			"Decision completed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "evaluate",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			logging.FieldDuration, logging.SinceMillis(start),
			"decision", string(result.Action),
			"reason", result.Reason,
		)

		return result, nil
	}

	if deadlineAllowsImmediateRun(workload, now) {
		result.Reason =
			"waiting would make the workload deadline infeasible; run now"

		log.Info(
			"Decision completed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "evaluate",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			logging.FieldDuration, logging.SinceMillis(start),
			"decision", string(result.Action),
			"reason", result.Reason,
		)

		return result, nil
	}

	if workload.MaxDelay == 0 {
		result.Reason =
			"workload does not permit additional scheduling delay"

		log.Info(
			"Decision completed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "evaluate",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			logging.FieldDuration, logging.SinceMillis(start),
			"decision", string(result.Action),
			"reason", result.Reason,
		)

		return result, nil
	}

	// Retrieve carbon forecast.
	carbonForecastStart := time.Now()

	log.V(1).Info(
		"Requesting carbon forecast",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "get_forecast",
		logging.FieldWorkload, workload.Name,
		logging.FieldSignal, signals.SignalTypeCarbon,
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusStarted,
	)

	carbonForecast, carbonErr := e.signalSource.GetForecastSignals(
		ctx,
		signals.SignalRequest{
			SignalType: signals.SignalTypeCarbon,
			Region:     region,
		},
		e.config.ForecastHorizon,
	)

	if carbonErr != nil {
		if err := ctx.Err(); err != nil {
			log.Error(
				err,
				"Carbon forecast request cancelled",
				logging.FieldComponent, logging.ComponentDecisionEngine,
				logging.FieldOperation, "get_forecast",
				logging.FieldWorkload, workload.Name,
				logging.FieldSignal, signals.SignalTypeCarbon,
				logging.FieldRegion, region,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(carbonForecastStart),
			)

			return Decision{}, err
		}

		log.Error(
			carbonErr,
			"Carbon forecast unavailable",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "get_forecast",
			logging.FieldWorkload, workload.Name,
			logging.FieldSignal, signals.SignalTypeCarbon,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(carbonForecastStart),
		)

		result.Metadata["forecast"] =
			"unavailable; current option selected"

		log.Info(
			"Decision completed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "evaluate",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			logging.FieldDuration, logging.SinceMillis(start),
			"decision", string(result.Action),
			"reason", result.Reason,
		)

		return result, nil
	}

	log.Info(
		"Carbon forecast retrieved",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "get_forecast",
		logging.FieldWorkload, workload.Name,
		logging.FieldSignal, signals.SignalTypeCarbon,
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(carbonForecastStart),
	)

	// Retrieve cost forecast.
	costForecastStart := time.Now()

	log.V(1).Info(
		"Requesting cost forecast",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "get_forecast",
		logging.FieldWorkload, workload.Name,
		logging.FieldSignal, signals.SignalTypeCost,
		logging.FieldRegion, signals.RegionAllIndia,
		logging.FieldStatus, logging.StatusStarted,
	)

	costForecast, costErr := e.signalSource.GetForecastSignals(
		ctx,
		signals.SignalRequest{
			SignalType: signals.SignalTypeCost,
			Region:     signals.RegionAllIndia,
		},
		e.config.ForecastHorizon,
	)

	if costErr != nil {
		if err := ctx.Err(); err != nil {
			log.Error(
				err,
				"Cost forecast request cancelled",
				logging.FieldComponent, logging.ComponentDecisionEngine,
				logging.FieldOperation, "get_forecast",
				logging.FieldWorkload, workload.Name,
				logging.FieldSignal, signals.SignalTypeCost,
				logging.FieldRegion, signals.RegionAllIndia,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(costForecastStart),
			)

			return Decision{}, err
		}

		log.Error(
			costErr,
			"Cost forecast unavailable",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "get_forecast",
			logging.FieldWorkload, workload.Name,
			logging.FieldSignal, signals.SignalTypeCost,
			logging.FieldRegion, signals.RegionAllIndia,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(costForecastStart),
		)

		result.Metadata["forecast"] =
			"unavailable; current option selected"

		log.Info(
			"Decision completed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "evaluate",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			logging.FieldDuration, logging.SinceMillis(start),
			"decision", string(result.Action),
			"reason", result.Reason,
		)

		return result, nil
	}

	log.Info(
		"Cost forecast retrieved",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "get_forecast",
		logging.FieldWorkload, workload.Name,
		logging.FieldSignal, signals.SignalTypeCost,
		logging.FieldRegion, signals.RegionAllIndia,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(costForecastStart),
	)

	result.Metadata["forecast"] = "evaluated"

	best := currentWindow
	latestStart := now.Add(workload.MaxDelay)

	deadlineStart := workload.Deadline.Add(
		-workload.ExpectedRuntime,
	)

	if deadlineStart.Before(latestStart) {
		latestStart = deadlineStart
	}

	costByDate := make(map[string]signals.Signal, len(costForecast))

	for _, item := range costForecast {
		if err := ctx.Err(); err != nil {
			log.Error(
				err,
				"Forecast evaluation cancelled",
				logging.FieldComponent, logging.ComponentDecisionEngine,
				logging.FieldOperation, "evaluate_forecast",
				logging.FieldWorkload, workload.Name,
				logging.FieldRegion, region,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(start),
			)

			return Decision{}, err
		}

		if isUsableCostForecast(
			item,
			now,
		) {
			dateKey := item.Timestamp.UTC().Format("2006-01-02")
			costByDate[dateKey] = item
		}
	}

	for _, carbon := range carbonForecast {
		if err := ctx.Err(); err != nil {
			log.Error(
				err,
				"Forecast evaluation cancelled",
				logging.FieldComponent, logging.ComponentDecisionEngine,
				logging.FieldOperation, "evaluate_forecast",
				logging.FieldWorkload, workload.Name,
				logging.FieldRegion, region,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(start),
			)

			return Decision{}, err
		}

		if !isUsableForecast(
			carbon,
			signals.SignalTypeCarbon,
			region,
			now,
		) {
			continue
		}

		startTime := carbon.Timestamp

		if !startTime.After(now) ||
			startTime.After(latestStart) ||
			startTime.Add(workload.ExpectedRuntime).
				After(workload.Deadline) {
			continue
		}

		dateKey := startTime.UTC().Format("2006-01-02")
		cost, ok := costByDate[dateKey]
		if !ok {
			continue
		}

		futureSet := currentSignals
		futureSet.Carbon = carbon
		futureSet.Cost = cost

		window, scoreErr := e.makeWindow(
			workload,
			futureSet,
			now,
			startTime,
			true,
		)

		if scoreErr != nil {
			log.V(1).Info(
				"Forecast window could not be scored",
				logging.FieldComponent, logging.ComponentDecisionEngine,
				logging.FieldOperation, "score_forecast_window",
				logging.FieldWorkload, workload.Name,
				logging.FieldRegion, region,
				logging.FieldStatus, logging.StatusFailure,
				"error", scoreErr.Error(),
			)

			continue
		}

		log.V(1).Info(
			"Forecast window scored",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "score_forecast_window",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			"score", window.Score.Total,
			"start_time", startTime,
		)

		if window.Score.Total < best.Score.Total {
			best = window
		}
	}

	if best.StartTime.After(now) {
		improvement := scoreImprovement(
			currentWindow.Score.Total,
			best.Score.Total,
		)

		log.V(1).Info(
			"Forecast window comparison completed",
			logging.FieldComponent, logging.ComponentDecisionEngine,
			logging.FieldOperation, "compare_windows",
			logging.FieldWorkload, workload.Name,
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusSuccess,
			"current_score", currentWindow.Score.Total,
			"best_forecast_score", best.Score.Total,
			"improvement", improvement,
		)

		if improvement >= e.config.MinimumImprovement {
			result.Action = ActionWait
			result.Reason = fmt.Sprintf(
				"future window improves the score by %.1f%%",
				improvement*100,
			)
			result.Selected = best
			result.RequeueAfter = best.StartTime.Sub(now)

			if result.RequeueAfter < time.Second {
				result.RequeueAfter = time.Second
			}

			log.Info(
				"Decision completed",
				logging.FieldComponent, logging.ComponentDecisionEngine,
				logging.FieldOperation, "evaluate",
				logging.FieldWorkload, workload.Name,
				logging.FieldRegion, region,
				logging.FieldStatus, logging.StatusSuccess,
				logging.FieldDuration, logging.SinceMillis(start),
				"decision", string(result.Action),
				"reason", result.Reason,
			)

			return result, nil
		}
	}

	result.Reason =
		"no future window provides the configured minimum improvement"

	log.Info(
		"Decision completed",
		logging.FieldComponent, logging.ComponentDecisionEngine,
		logging.FieldOperation, "evaluate",
		logging.FieldWorkload, workload.Name,
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(start),
		"decision", string(result.Action),
		"reason", result.Reason,
	)

	return result, nil
}

func validateWorkload(w WorkloadRequest) error {
	if strings.TrimSpace(w.Name) == "" || strings.TrimSpace(w.Namespace) == "" {
		return fmt.Errorf("workload name and namespace are required")
	}
	if strings.TrimSpace(w.Region) == "" {
		return fmt.Errorf("workload region is required")
	}
	if w.ExpectedRuntime <= 0 {
		return fmt.Errorf("expected runtime must be positive")
	}
	if w.Deadline.IsZero() {
		return fmt.Errorf("workload deadline is required")
	}
	if w.MaxDelay < 0 {
		return fmt.Errorf("maximum delay cannot be negative")
	}
	if math.IsNaN(w.EstimatedEnergyWh) ||
		math.IsInf(w.EstimatedEnergyWh, 0) ||
		w.EstimatedEnergyWh <= 0 {
		return fmt.Errorf("estimated energy (Wh) must be finite and positive")
	}
	return nil
}

func validateSignalSet(s signals.SignalSet, region string, now time.Time) error {
	checks := []struct {
		name   string
		signal signals.Signal
		typ    string
	}{
		{"carbon", s.Carbon, signals.SignalTypeCarbon},
		{"cost", s.Cost, signals.SignalTypeCost},
		{"demand cap", s.DemandCap, signals.SignalTypeDemandCap},
	}

	for _, c := range checks {
		if !strings.EqualFold(strings.TrimSpace(c.signal.SignalType), c.typ) {
			return fmt.Errorf(
				"%s signal has type %q, expected %q",
				c.name,
				c.signal.SignalType,
				c.typ,
			)
		}
		expectedRegion := region
		if c.typ == signals.SignalTypeCost {
			expectedRegion = signals.RegionAllIndia
		}
		if !sameRegion(c.signal.Region, expectedRegion) {
			return fmt.Errorf(
				"%s signal region %q does not match expected region %q",
				c.name,
				c.signal.Region,
				expectedRegion,
			)
		}
		if math.IsNaN(c.signal.Value) ||
			math.IsInf(c.signal.Value, 0) ||
			c.signal.Value < 0 {
			return fmt.Errorf("%s signal value must be finite and non-negative", c.name)
		}
		if c.signal.ValidUntil.IsZero() || !c.signal.ValidUntil.After(now) {
			return fmt.Errorf("%s signal is expired or has no valid_until", c.name)
		}
	}

	demand := s.DemandCap
	if math.IsNaN(demand.Cap) ||
		math.IsInf(demand.Cap, 0) ||
		demand.Cap <= 0 {
		return fmt.Errorf("demand cap must be finite and positive")
	}
	if math.IsNaN(demand.Utilization) ||
		math.IsInf(demand.Utilization, 0) ||
		demand.Utilization < 0 {
		return fmt.Errorf("demand utilization must be finite and non-negative")
	}

	expectedUtilization := demand.Value / demand.Cap
	if math.Abs(demand.Utilization-expectedUtilization) > 1e-9 {
		return fmt.Errorf("demand utilization must equal value/cap")
	}

	expectedStatus := "WITHIN_CAP"
	if demand.Value > demand.Cap {
		expectedStatus = "EXCEEDED"
	}
	if !strings.EqualFold(strings.TrimSpace(demand.Status), expectedStatus) {
		return fmt.Errorf("demand status must be %s", expectedStatus)
	}

	return nil
}

func isUsableForecast(
	signal signals.Signal,
	signalType string,
	region string,
	now time.Time,
) bool {
	if !signal.IsForecast ||
		!strings.EqualFold(strings.TrimSpace(signal.SignalType), signalType) ||
		!sameRegion(signal.Region, region) ||
		signal.Timestamp.IsZero() ||
		!signal.Timestamp.After(now) ||
		signal.ValidUntil.IsZero() ||
		!signal.ValidUntil.After(now) {
		return false
	}

	if math.IsNaN(signal.Value) ||
		math.IsInf(signal.Value, 0) ||
		signal.Value < 0 {
		return false
	}

	return true
}

func isUsableCostForecast(signal signals.Signal, now time.Time) bool {
	if !signal.IsForecast ||
		!strings.EqualFold(strings.TrimSpace(signal.SignalType), signals.SignalTypeCost) ||
		!sameRegion(signal.Region, signals.RegionAllIndia) ||
		signal.Timestamp.IsZero() ||
		signal.ValidUntil.IsZero() ||
		!signal.ValidUntil.After(now) {
		return false
	}

	if math.IsNaN(signal.Value) ||
		math.IsInf(signal.Value, 0) ||
		signal.Value < 0 {
		return false
	}

	forecastDate := signal.Timestamp.UTC()
	currentDate := now.UTC()

	// Cost forecasts represent a delivery date, not an exact time.
	// Today's forecast is still usable even though its timestamp
	// (00:00 UTC) may already be before now.
	if forecastDate.Year() < currentDate.Year() {
		return false
	}

	if forecastDate.Year() == currentDate.Year() &&
		forecastDate.YearDay() < currentDate.YearDay() {
		return false
	}
	return true
}

func (e *Engine) makeWindow(
	w WorkloadRequest,
	set signals.SignalSet,
	now, start time.Time,
	future bool,
) (ExecutionWindow, error) {
	carbonImpact := w.EstimatedEnergyWh * set.Carbon.Value / 1000
	if math.IsNaN(carbonImpact) || math.IsInf(carbonImpact, 0) {
		return ExecutionWindow{}, fmt.Errorf("carbon impact is not finite")
	}

	carbonScore := clamp01(carbonImpact / e.config.Bounds.MaxCarbonImpact)
	energyScore := clamp01(w.EstimatedEnergyWh / e.config.Bounds.MaxEnergyWh)
	costScore := clamp01(set.Cost.Value / e.config.Bounds.MaxCostPerKWh)

	delayScore := 0.0
	if future && w.MaxDelay > 0 {
		delayScore = clamp01(start.Sub(now).Seconds() / w.MaxDelay.Seconds())
	}

	weights := e.config.Weights
	total := scoreWindow(
		weights,
		carbonScore,
		energyScore,
		costScore,
		delayScore,
	)

	return ExecutionWindow{
		StartTime: start,
		EndTime:   start.Add(w.ExpectedRuntime),
		Score: ScoreBreakdown{
			CarbonImpact: carbonScore,
			Energy:       energyScore,
			Cost:         costScore,
			Delay:        delayScore,
			Total:        total,
		},
	}, nil
}

func deadlineAllowsImmediateRun(w WorkloadRequest, now time.Time) bool {
	return !w.Deadline.After(now.Add(w.ExpectedRuntime))
}

func scoreImprovement(current, future float64) float64 {
	if current <= 0 {
		return 0
	}
	improvement := (current - future) / current
	if improvement < 0 {
		return 0
	}
	return improvement
}

func normalizeRegion(region string) string {
	region = strings.ToUpper(strings.TrimSpace(region))
	if region == "ALL INDIA" {
		return signals.RegionAllIndia
	}
	return region
}

func sameRegion(a, b string) bool {
	return normalizeRegion(a) == normalizeRegion(b)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
