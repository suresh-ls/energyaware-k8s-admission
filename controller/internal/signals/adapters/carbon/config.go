package carbon

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://api.electricitymaps.com"
	DefaultZone    = "IN-SO"
	ProviderName   = "electricity_maps"
	CarbonUnit     = "gCO2eq/kWh"
)

type Config struct {
	APIToken       string
	Zone           string
	BaseURL        string
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

func (c *Config) validate() error {
	c.APIToken = strings.TrimSpace(c.APIToken)
	if c.APIToken == "" {
		return fmt.Errorf("Electricity Maps API token is required")
	}

	c.Zone = strings.TrimSpace(c.Zone)
	if c.Zone == "" {
		c.Zone = DefaultZone
	}

	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}

	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 15 * time.Second
	}

	return nil
}
