package signals

import (
	"fmt"
	"strings"
	"time"
)

// CloudSpotUnit is deliberately distinct from the electricity cost unit.
const CloudSpotUnit = "USD/instance-hour"

// CloudSpotPool identifies one AWS Linux spot market and is safe as a cache key.
type CloudSpotPool struct {
	Provider         string `json:"provider"`
	Region           string `json:"region"`
	AvailabilityZone string `json:"availability_zone"`
	InstanceType     string `json:"instance_type"`
	Product          string `json:"product"`
}

// Canonical returns the pool identity used by adapters, callers, and the cache.
func (p CloudSpotPool) Canonical() CloudSpotPool {
	p.Provider = strings.ToLower(strings.TrimSpace(p.Provider))
	p.Region = strings.ToLower(strings.TrimSpace(p.Region))
	p.AvailabilityZone = strings.ToLower(strings.TrimSpace(p.AvailabilityZone))
	p.InstanceType = strings.ToLower(strings.TrimSpace(p.InstanceType))
	p.Product = strings.TrimSpace(p.Product)
	return p
}

// Validate limits the initial implementation to explicitly selected Linux pools.
func (p CloudSpotPool) Validate() error {
	p = p.Canonical()
	if p.Provider != "aws" || p.Region == "" || p.InstanceType == "" ||
		p.Product != "Linux/UNIX" || !strings.HasPrefix(p.AvailabilityZone, p.Region) ||
		len(p.AvailabilityZone) <= len(p.Region) {
		return fmt.Errorf("cloud spot requires an AWS region, availability zone, instance type, and Linux/UNIX product")
	}
	return nil
}

// ValidateCloudSpotSignal also protects engine callers that bypass the handler.
func ValidateCloudSpotSignal(s Signal, pool CloudSpotPool, now time.Time) error {
	if err := pool.Validate(); err != nil {
		return err
	}
	if s.CloudPool.Canonical() != pool.Canonical() ||
		!strings.EqualFold(s.Region, pool.Region) || s.SignalType != SignalTypeCloudSpot ||
		s.Unit != CloudSpotUnit || s.Value < 0 || s.IsForecast || s.CollectedAt.IsZero() {
		return fmt.Errorf("cloud spot quote does not match the requested pool or current USD instance-hour contract")
	}
	return validateSignal(s, now, 5*time.Minute)
}
