package carbon

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestCarbonFailureLogsDoNotExposeResponseBody(t *testing.T) {
	const privateMarker = "PRIVATE_PROVIDER_DETAIL_12345"
	const expectedStatus = http.StatusUnauthorized

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, privateMarker, expectedStatus)
	}))
	defer server.Close()

	var logOutput bytes.Buffer

	log.SetLogger(zap.New(
		zap.UseDevMode(true),
		zap.WriteTo(&logOutput),
		zap.Level(zapcore.ErrorLevel),
	))

	client, err := NewClient(Config{
		APIToken:   "test-token",
		Zone:       "IN-SO",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	t.Run("current carbon signal", func(t *testing.T) {
		logOutput.Reset()

		_, err := client.GetCurrent(context.Background(), "IN-SO")
		if err == nil {
			t.Fatal("expected current signal retrieval to fail")
		}

		output := logOutput.String()

		if strings.Contains(output, privateMarker) {
			t.Errorf("current signal log exposed provider response body: %s", output)
		}
		if !strings.Contains(output, "provider_http_error") {
			t.Errorf("current signal log missing safe error category: %s", output)
		}
		if !strings.Contains(output, "401") {
			t.Errorf("current signal log missing HTTP status: %s", output)
		}
	})

	t.Run("carbon forecast", func(t *testing.T) {
		logOutput.Reset()

		_, err := client.GetForecast(context.Background(), "IN-SO", 24*time.Hour)
		if err == nil {
			t.Fatal("expected forecast retrieval to fail")
		}

		output := logOutput.String()

		if strings.Contains(output, privateMarker) {
			t.Errorf("forecast log exposed provider response body: %s", output)
		}
		if !strings.Contains(output, "provider_http_error") {
			t.Errorf("forecast log missing safe error category: %s", output)
		}
		if !strings.Contains(output, "401") {
			t.Errorf("forecast log missing HTTP status: %s", output)
		}
	})
}
