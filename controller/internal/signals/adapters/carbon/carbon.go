package carbon

import (
	"context"
	"fmt"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

type Provider struct {
	client *Client
}

func NewProvider(client *Client) *Provider {
	return &Provider{
		client: client,
	}
}

func (p *Provider) GetCurrentSignal(
	ctx context.Context,
	request signals.SignalRequest,
) (signals.Signal, error) {
	if p == nil || p.client == nil {
		return signals.Signal{}, fmt.Errorf("carbon provider is not configured")
	}

	if request.SignalType != signals.SignalTypeCarbon {
		return signals.Signal{}, fmt.Errorf(
			"unsupported signal type: %s",
			request.SignalType,
		)
	}

	return p.client.GetCurrent(ctx, request.Region)
}

func (p *Provider) GetForecastSignals(
	ctx context.Context,
	request signals.SignalRequest,
	horizon time.Duration,
) ([]signals.Signal, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("carbon provider is not configured")
	}

	if request.SignalType != signals.SignalTypeCarbon {
		return nil, fmt.Errorf(
			"unsupported signal type: %s",
			request.SignalType,
		)
	}

	return p.client.GetForecast(ctx, request.Region, horizon)
}
