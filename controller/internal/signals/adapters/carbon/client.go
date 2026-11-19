package carbon

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Client struct {
	config Config
	http   *http.Client
}

func NewClient(config Config) (*Client, error) {
	logger := log.Log.WithName("carbon")

	if err := config.validate(); err != nil {
		logger.Error(err, "Failed to initialize Carbon client",
			"provider", ProviderName,
		)
		return nil, err
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: config.RequestTimeout,
		}
	}

	client := &Client{
		config: config,
		http:   httpClient,
	}

	logger.Info("Carbon client initialized",
		"provider", ProviderName,
		"zone", config.Zone,
		"timeout", config.RequestTimeout.String(),
	)

	return client, nil
}

func (c *Client) newRequest(ctx context.Context, endpoint string) (*http.Request, error) {
	if c == nil {
		return nil, fmt.Errorf("carbon client is nil")
	}
	if ctx == nil {
		return nil, fmt.Errorf("request context is nil")
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.config.BaseURL+endpoint,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create Electricity Maps request: %w", err)
	}

	req.Header.Set("auth-token", c.config.APIToken)
	return req, nil
}

func (c *Client) now() time.Time {
	return time.Now().UTC()
}
