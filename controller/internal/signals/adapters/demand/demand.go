package demand

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) GetCurrentSignal(
	ctx context.Context,
	request signals.SignalRequest,
) (signals.Signal, error) {
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