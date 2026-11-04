package carbon

import (
	"context"
	"os"
	"testing"
	"time"
)

func getLiveClient(t *testing.T) *Client {
	t.Helper()

	token := os.Getenv("ELECTRICITY_MAPS_API_TOKEN")
	if token == "" {
		t.Skip("ELECTRICITY_MAPS_API_TOKEN is not set")
	}

	zone := os.Getenv("ELECTRICITY_MAPS_ZONE")
	if zone == "" {
		zone = DefaultZone
	}

	client, err := NewClient(Config{
		APIToken: token,
		Zone:     zone,
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}

	return client
}

func TestLiveElectricityMapsCurrent(t *testing.T) {
	client := getLiveClient(t)

	signal, err := client.GetCurrent(context.Background(), "")
	if err != nil {
		t.Fatalf("Live current API request failed: %v", err)
	}

	if signal.Value < 0 {
		t.Fatalf("invalid carbon intensity: %v", signal.Value)
	}

	t.Logf("Current carbon intensity: %.2f %s", signal.Value, signal.Unit)
	t.Logf("Region: %s", signal.Region)
	t.Logf("Timestamp: %s", signal.Timestamp.Format(time.RFC3339))
	t.Logf("Quality: %s", signal.Quality)
}

func TestLiveElectricityMapsForecast(t *testing.T) {
	client := getLiveClient(t)

	signals, err := client.GetForecast(context.Background(), "", 24*time.Hour)
	if err != nil {
		t.Fatalf("Live forecast API request failed: %v", err)
	}

	if len(signals) == 0 {
		t.Fatal("API returned an empty forecast")
	}

	t.Logf("Forecast entries received: %d", len(signals))

	for i, signal := range signals {
		if signal.Value < 0 {
			t.Errorf("invalid intensity at forecast entry %d: %v", i, signal.Value)
		}
	}

	t.Logf("First forecast value: %.2f %s", signals[0].Value, signals[0].Unit)
	t.Logf("Last forecast value: %.2f %s", signals[len(signals)-1].Value, signals[len(signals)-1].Unit)
}
