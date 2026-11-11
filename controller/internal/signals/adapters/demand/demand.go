package demand

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	ctrl "sigs.k8s.io/controller-runtime"
)

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) GetCurrentSignal(
	ctx context.Context,
	request signals.SignalRequest,
) (output signals.Signal, retErr error) {
	started := time.Now()

	logger := ctrl.Log.WithName("demand-provider").WithValues(
		"component", "demand-provider",
		"operation", "get_current_signal",
		"signal_type", request.SignalType,
		"provider", "dummy_demand_cap",
		"region", strings.TrimSpace(request.Region),
	)

	logger.Info("operation started")

	defer func() {
		duration := time.Since(started).Milliseconds()

		if retErr != nil {
			logger.Error(retErr, "operation failed",
				"status", "failure",
				"duration_ms", duration,
			)
		} else {
			logger.Info("operation completed",
				"status", "success",
				"duration_ms", duration,
			)
		}
	}()
	if p == nil {
		return signals.Signal{}, fmt.Errorf("demand cap provider must not be nil")
	}

	if ctx == nil {
		return signals.Signal{}, fmt.Errorf("context must not be nil")
	}

	if request.SignalType != signals.SignalTypeDemandCap {
		return signals.Signal{}, fmt.Errorf(
			"unsupported signal type %q: expected %q",
			request.SignalType,
			signals.SignalTypeDemandCap,
		)
	}

	region := strings.TrimSpace(request.Region)
	if region == "" {
		return signals.Signal{}, fmt.Errorf("region must not be empty")
	}

	now := time.Now().UTC()

	return signals.Signal{
		SignalType:  signals.SignalTypeDemandCap,
		Provider:    "DummyDemandCap",
		Region:      region,
		Value:       80,
		Unit:        "percent",
		Timestamp:   now,
		CollectedAt: now,
		ValidUntil:  now.Add(5 * time.Minute),
		IsForecast:  false,
		Quality:     "simulated",

		Cap:         100,
		Utilization: 80,
		Status:      "within_cap",
	}, nil
}

var _ signals.SignalProvider = (*Provider)(nil)
