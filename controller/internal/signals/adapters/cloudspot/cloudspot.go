// Package cloudspot provides current AWS EC2 spot quotes, not price forecasts.
package cloudspot

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

// Client is the read-only EC2 operation required by this adapter.
type Client interface {
	DescribeSpotPriceHistory(context.Context, *ec2.DescribeSpotPriceHistoryInput,
		...func(*ec2.Options)) (*ec2.DescribeSpotPriceHistoryOutput, error)
}

// Config fixes one pool for the lifetime of a provider. TTL is a refresh policy,
// not a guarantee that AWS keeps the quoted price unchanged.
type Config struct {
	Pool    signals.CloudSpotPool
	TTL     time.Duration
	Timeout time.Duration
	Now     func() time.Time
}

// Provider implements the shared current-signal contract.
type Provider struct {
	client Client
	config Config
	name   string
}

// NewProvider creates a provider for one explicitly selected pool.
func NewProvider(client Client, cfg Config) (*Provider, error) {
	if client == nil {
		return nil, fmt.Errorf("EC2 client is required")
	}
	cfg.Pool = cfg.Pool.Canonical()
	if err := cfg.Pool.Validate(); err != nil {
		return nil, err
	}
	if cfg.TTL == 0 {
		cfg.TTL = 5 * time.Minute
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.TTL <= 0 || cfg.TTL > 5*time.Minute || cfg.Timeout <= 0 {
		return nil, fmt.Errorf("spot TTL must be in (0,5m] and timeout must be positive")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Provider{client: client, config: cfg, name: "AWS-EC2-Spot"}, nil
}

// GetCurrentSignal selects the newest matching price effective at lookup time.
// A price's effective timestamp can be old even when the lookup is fresh.
func (p *Provider) GetCurrentSignal(ctx context.Context, request signals.SignalRequest) (signals.Signal, error) {
	if p == nil || ctx == nil {
		return signals.Signal{}, fmt.Errorf("provider and context are required")
	}
	pool := p.config.Pool
	if request.SignalType != signals.SignalTypeCloudSpot || request.CloudPool.Canonical() != pool ||
		request.Region != pool.Region {
		return signals.Signal{}, fmt.Errorf("cloud spot request does not match configured pool")
	}
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return signals.Signal{}, err
	}
	now := p.config.Now().UTC()
	input := &ec2.DescribeSpotPriceHistoryInput{
		AvailabilityZone:    aws.String(pool.AvailabilityZone),
		InstanceTypes:       []types.InstanceType{types.InstanceType(pool.InstanceType)},
		ProductDescriptions: []string{pool.Product},
		StartTime:           aws.Time(now),
		EndTime:             aws.Time(now),
	}
	var selected time.Time
	var price float64
	seenTokens := make(map[string]bool)
	for {
		output, err := p.client.DescribeSpotPriceHistory(ctx, input)
		if err != nil {
			// Avoid exposing HTTP request details or credentials in controller logs.
			if ctx.Err() != nil {
				return signals.Signal{}, ctx.Err()
			}
			return signals.Signal{}, fmt.Errorf("AWS spot price lookup failed")
		}
		if output == nil {
			return signals.Signal{}, fmt.Errorf("AWS spot lookup returned no response")
		}
		for _, record := range output.SpotPriceHistory {
			if aws.ToString(record.AvailabilityZone) != pool.AvailabilityZone ||
				string(record.InstanceType) != pool.InstanceType || string(record.ProductDescription) != pool.Product {
				continue
			}
			effective := aws.ToTime(record.Timestamp)
			value, parseErr := strconv.ParseFloat(aws.ToString(record.SpotPrice), 64)
			if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 ||
				effective.IsZero() || effective.After(now) {
				return signals.Signal{}, fmt.Errorf("AWS returned an invalid spot price record")
			}
			if effective.After(selected) {
				selected, price = effective, value
			}
		}
		token := aws.ToString(output.NextToken)
		if token == "" {
			break
		}
		if seenTokens[token] || len(seenTokens) >= 100 {
			return signals.Signal{}, fmt.Errorf("AWS spot pagination did not terminate")
		}
		seenTokens[token] = true
		input.NextToken = aws.String(token)
	}
	if err := ctx.Err(); err != nil {
		return signals.Signal{}, err
	}
	if selected.IsZero() {
		return signals.Signal{}, fmt.Errorf("no current spot quote for configured pool")
	}
	return signals.Signal{
		SignalType: signals.SignalTypeCloudSpot, Provider: p.name,
		Region: pool.Region, CloudPool: pool, Value: price, Unit: signals.CloudSpotUnit,
		Timestamp: selected, CollectedAt: now, ValidUntil: now.Add(p.config.TTL), Quality: "FRESH",
	}, nil
}

var _ signals.SignalProvider = (*Provider)(nil)
