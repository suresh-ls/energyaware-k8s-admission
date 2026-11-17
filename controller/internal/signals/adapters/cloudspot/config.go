package cloudspot

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

// FromEnvironment leaves cloud pricing disabled unless CLOUD_SPOT_MODE is set.
// AWS mode uses the SDK's default credential chain; replay mode makes no AWS calls.
func FromEnvironment(ctx context.Context, httpClient *http.Client) (
	*signals.CloudSpotPool, signals.SignalProvider, error) {
	mode := strings.TrimSpace(os.Getenv("CLOUD_SPOT_MODE"))
	if mode == "" {
		return nil, nil, nil
	}
	if mode != "aws" && mode != "replay" {
		return nil, nil, fmt.Errorf("CLOUD_SPOT_MODE must be aws, replay, or unset")
	}
	pool := signals.CloudSpotPool{
		Provider: "aws", Region: os.Getenv("CLOUD_SPOT_REGION"),
		AvailabilityZone: os.Getenv("CLOUD_SPOT_AVAILABILITY_ZONE"),
		InstanceType:     os.Getenv("CLOUD_SPOT_INSTANCE_TYPE"), Product: "Linux/UNIX",
	}.Canonical()
	if err := pool.Validate(); err != nil {
		return nil, nil, err
	}
	cfg := Config{Pool: pool}
	if mode == "replay" {
		data, err := os.ReadFile(os.Getenv("CLOUD_SPOT_TRACE_FILE"))
		if err != nil {
			return nil, nil, fmt.Errorf("read CLOUD_SPOT_TRACE_FILE: %w", err)
		}
		provider, err := NewReplayProvider(data, cfg)
		return &pool, provider, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	config, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(pool.Region), awsconfig.WithHTTPClient(httpClient))
	if err != nil {
		return nil, nil, fmt.Errorf("load AWS spot-price client configuration")
	}
	provider, err := NewProvider(ec2.NewFromConfig(config), cfg)
	return &pool, provider, err
}
