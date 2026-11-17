package signals

import (
	"context"
	"testing"
	"time"
)

type poolPriceProvider struct {
	now   func() time.Time
	calls int
}

func (p *poolPriceProvider) GetCurrentSignal(_ context.Context, request SignalRequest) (Signal, error) {
	p.calls++
	price := .1
	if request.CloudPool.AvailabilityZone == "ap-south-1b" {
		price = .2
	}
	return Signal{SignalType: SignalTypeCloudSpot, Provider: "test", Region: request.Region, CloudPool: request.CloudPool,
		Value: price, Unit: CloudSpotUnit, Timestamp: p.now().Add(-24 * time.Hour), CollectedAt: p.now(),
		ValidUntil: p.now().Add(time.Minute), Quality: "FRESH"}, nil
}

func TestCloudSpotCacheIsolationAndRefresh(t *testing.T) {
	now := time.Now().UTC()
	p := &poolPriceProvider{now: func() time.Time { return now }}
	h := NewHandlerWithOptions(p, HandlerOptions{Now: p.now})
	pool := CloudSpotPool{Provider: "aws", Region: "ap-south-1", AvailabilityZone: "ap-south-1a",
		InstanceType: "c5.xlarge", Product: "Linux/UNIX"}
	a := SignalRequest{SignalType: SignalTypeCloudSpot, Region: "AP-SOUTH-1", CloudPool: pool}
	b := a
	b.CloudPool.AvailabilityZone = "ap-south-1b"
	for _, req := range []SignalRequest{a, a, b, b} {
		quote, err := h.GetSignal(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		want := .1
		if req.CloudPool.AvailabilityZone == "ap-south-1b" {
			want = .2
		}
		if quote.Value != want || quote.Region != "ap-south-1" {
			t.Fatalf("wrong cached pool price: %+v", quote)
		}
	}
	if p.calls != 2 {
		t.Fatalf("got %d provider calls, want 2", p.calls)
	}
	now = now.Add(time.Minute)
	if _, err := h.GetSignal(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if p.calls != 3 {
		t.Fatal("expired quote was not refreshed")
	}
}

func TestCloudSpotRejectsMismatchedProviderPool(t *testing.T) {
	now := time.Now().UTC()
	pool := CloudSpotPool{Provider: "aws", Region: "ap-south-1", AvailabilityZone: "ap-south-1a",
		InstanceType: "c5.xlarge", Product: "Linux/UNIX"}
	quote, err := (&poolPriceProvider{now: func() time.Time { return now }}).GetCurrentSignal(context.Background(),
		SignalRequest{SignalType: SignalTypeCloudSpot, Region: pool.Region, CloudPool: pool})
	if err != nil {
		t.Fatal(err)
	}
	other := pool
	other.InstanceType = "m5.large"
	h := NewHandler(mockProvider{signal: quote})
	if _, err := h.GetSignal(context.Background(), SignalRequest{SignalType: SignalTypeCloudSpot,
		Region: pool.Region, CloudPool: other}); err == nil {
		t.Fatal("wrong pool quote accepted")
	}
}
