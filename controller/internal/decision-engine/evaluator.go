package decisionengine

import "strings"

// Demand capping is a hard constraint. A workload must not be scheduled while
// the current demand cap is exceeded unless deadline feasibility requires RUN.
func demandCapExceeded(utilization float64, status string) bool {
	return utilization >= 1 || strings.EqualFold(strings.TrimSpace(status), "EXCEEDED")
}
