package cloudspot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

type fakeEC2 struct {
	pages  []*ec2.DescribeSpotPriceHistoryOutput
	inputs []ec2.DescribeSpotPriceHistoryInput
	err    error
}

func (f *fakeEC2) DescribeSpotPriceHistory(ctx context.Context, input *ec2.DescribeSpotPriceHistoryInput,
	_ ...func(*ec2.Options)) (*ec2.DescribeSpotPriceHistoryOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.inputs = append(f.inputs, *input)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return nil, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func testPool() signals.CloudSpotPool {
	return signals.CloudSpotPool{Provider: "aws", Region: "ap-south-1", AvailabilityZone: "ap-south-1a",
		InstanceType: "c5.xlarge", Product: "Linux/UNIX"}
}

func record(at time.Time, price string) types.SpotPrice {
	pool := testPool()
	return types.SpotPrice{AvailabilityZone: aws.String(pool.AvailabilityZone),
		InstanceType: types.InstanceType(pool.InstanceType), ProductDescription: types.RIProductDescription(pool.Product),
		SpotPrice: aws.String(price), Timestamp: aws.Time(at)}
}

func request() signals.SignalRequest {
	return signals.SignalRequest{SignalType: signals.SignalTypeCloudSpot, Region: testPool().Region, CloudPool: testPool()}
}

func TestCloudSpotLatestMatchingPriceAcrossPages(t *testing.T) {
	now := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)
	other := record(now.Add(-time.Minute), "999")
	other.AvailabilityZone = aws.String("ap-south-1b")
	client := &fakeEC2{pages: []*ec2.DescribeSpotPriceHistoryOutput{
		{SpotPriceHistory: []types.SpotPrice{record(now.Add(-48*time.Hour), "0.4"), other}, NextToken: aws.String("page2")},
		{SpotPriceHistory: []types.SpotPrice{record(now.Add(-24*time.Hour), "0.2"), record(now.Add(-72*time.Hour), "0.7")}},
	}}
	p, err := NewProvider(client, Config{Pool: testPool(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	quote, err := p.GetCurrentSignal(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if quote.Value != .2 || !quote.Timestamp.Equal(now.Add(-24*time.Hour)) ||
		!quote.CollectedAt.Equal(now) || !quote.ValidUntil.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("incorrect price or freshness: %+v", quote)
	}
	if err := signals.ValidateCloudSpotSignal(quote, testPool(), now); err != nil {
		t.Fatal(err)
	}
	if len(client.inputs) != 2 || aws.ToString(client.inputs[1].NextToken) != "page2" ||
		!aws.ToTime(client.inputs[0].StartTime).Equal(now) ||
		aws.ToString(client.inputs[0].AvailabilityZone) != testPool().AvailabilityZone ||
		string(client.inputs[0].InstanceTypes[0]) != testPool().InstanceType {
		t.Fatalf("incorrect pool or pagination request: %+v", client.inputs)
	}
}

func TestCloudSpotRejectsBadResponses(t *testing.T) {
	now := time.Now().UTC()
	for _, price := range []string{"NaN", "+Inf", "-1", "garbage", ""} {
		t.Run(price, func(t *testing.T) {
			p, err := NewProvider(&fakeEC2{pages: []*ec2.DescribeSpotPriceHistoryOutput{
				{SpotPriceHistory: []types.SpotPrice{record(now, price)}},
			}}, Config{Pool: testPool(), Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.GetCurrentSignal(context.Background(), request()); err == nil {
				t.Fatal("expected invalid quote")
			}
		})
	}
	for _, page := range []*ec2.DescribeSpotPriceHistoryOutput{
		nil, {}, {SpotPriceHistory: []types.SpotPrice{record(now.Add(time.Minute), "0.2")}},
		{SpotPriceHistory: []types.SpotPrice{record(time.Time{}, "0.2")}},
	} {
		p, err := NewProvider(&fakeEC2{pages: []*ec2.DescribeSpotPriceHistoryOutput{page}}, Config{Pool: testPool()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.GetCurrentSignal(context.Background(), request()); err == nil {
			t.Fatal("expected invalid response")
		}
	}
}

func TestCloudSpotPaginationAndCancellation(t *testing.T) {
	client := &fakeEC2{pages: []*ec2.DescribeSpotPriceHistoryOutput{
		{NextToken: aws.String("same")}, {NextToken: aws.String("same")},
	}}
	p, err := NewProvider(client, Config{Pool: testPool()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetCurrentSignal(context.Background(), request()); err == nil {
		t.Fatal("expected pagination error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetCurrentSignal(ctx, request()); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	wrong := request()
	wrong.CloudPool.InstanceType = "m5.large"
	if _, err := p.GetCurrentSignal(context.Background(), wrong); err == nil {
		t.Fatal("expected pool mismatch")
	}
}

func TestCloudSpotReplayHasNoFutureDataLeakage(t *testing.T) {
	data := []byte(`{"pool":{"provider":"aws","region":"ap-south-1","availability_zone":"ap-south-1a",
"instance_type":"c5.xlarge","product":"Linux/UNIX"},"records":[
{"timestamp":"2026-11-17T00:00:00Z","price_usd":"0.4"},
{"timestamp":"2026-11-17T02:00:00Z","price_usd":"0.1"}]}`)
	now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	p, err := NewReplayProvider(data, Config{Pool: testPool(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	quote, err := p.GetCurrentSignal(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if quote.Value != .4 || quote.Provider != "AWS-EC2-Spot-Replay" {
		t.Fatalf("unexpected replay: %+v", quote)
	}
	now = now.Add(2 * time.Hour)
	quote, err = p.GetCurrentSignal(context.Background(), request())
	if err != nil || quote.Value != .1 {
		t.Fatalf("unexpected replay: %+v %v", quote, err)
	}
}

func TestCloudSpotEnvironmentOptIn(t *testing.T) {
	t.Setenv("CLOUD_SPOT_MODE", "")
	pool, provider, err := FromEnvironment(context.Background(), nil)
	if err != nil || pool != nil || provider != nil {
		t.Fatalf("feature should be disabled: %v", err)
	}
	t.Setenv("CLOUD_SPOT_MODE", "invalid")
	if _, _, err := FromEnvironment(context.Background(), nil); err == nil {
		t.Fatal("expected mode validation")
	}
	t.Setenv("CLOUD_SPOT_MODE", "replay")
	t.Setenv("CLOUD_SPOT_REGION", testPool().Region)
	t.Setenv("CLOUD_SPOT_AVAILABILITY_ZONE", testPool().AvailabilityZone)
	t.Setenv("CLOUD_SPOT_INSTANCE_TYPE", testPool().InstanceType)
	path := filepath.Join(t.TempDir(), "trace.json")
	if err := os.WriteFile(path, []byte(`{"pool":{"provider":"aws","region":"ap-south-1",
"availability_zone":"ap-south-1a","instance_type":"c5.xlarge","product":"Linux/UNIX"},
"records":[{"timestamp":"2026-01-01T00:00:00Z","price_usd":"0.2"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUD_SPOT_TRACE_FILE", path)
	pool, provider, err = FromEnvironment(context.Background(), nil)
	if err != nil || pool == nil || provider == nil {
		t.Fatalf("replay configuration failed: %v", err)
	}
}
