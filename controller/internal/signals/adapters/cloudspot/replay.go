package cloudspot

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

// ReplayTrace is a deterministic, explicitly labeled simulation input.
// Prices take effect at Timestamp and remain effective until the next record.
type ReplayTrace struct {
	Pool    signals.CloudSpotPool `json:"pool"`
	Records []ReplayRecord        `json:"records"`
}

// ReplayRecord holds a synthetic or recorded price change, never a prediction.
type ReplayRecord struct {
	Timestamp time.Time `json:"timestamp"`
	PriceUSD  string    `json:"price_usd"`
}

type replayClient struct{ trace ReplayTrace }

// NewReplayProvider uses the same selection and validation as live AWS lookups.
func NewReplayProvider(data []byte, cfg Config) (*Provider, error) {
	var trace ReplayTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		return nil, fmt.Errorf("decode spot replay: %w", err)
	}
	if trace.Pool.Canonical() != cfg.Pool.Canonical() || len(trace.Records) == 0 {
		return nil, fmt.Errorf("spot replay pool must match configuration and contain price records")
	}
	p, err := NewProvider(&replayClient{trace: trace}, cfg)
	if err == nil {
		p.name = "AWS-EC2-Spot-Replay"
	}
	return p, err
}

func (r *replayClient) DescribeSpotPriceHistory(ctx context.Context, input *ec2.DescribeSpotPriceHistoryInput,
	_ ...func(*ec2.Options)) (*ec2.DescribeSpotPriceHistoryOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pool := r.trace.Pool.Canonical()
	output := &ec2.DescribeSpotPriceHistoryOutput{}
	for _, record := range r.trace.Records {
		if record.Timestamp.After(aws.ToTime(input.EndTime)) {
			continue
		}
		output.SpotPriceHistory = append(output.SpotPriceHistory, types.SpotPrice{
			AvailabilityZone: aws.String(pool.AvailabilityZone), InstanceType: types.InstanceType(pool.InstanceType),
			ProductDescription: types.RIProductDescription(pool.Product), SpotPrice: aws.String(record.PriceUSD),
			Timestamp: aws.Time(record.Timestamp),
		})
	}
	return output, nil
}
