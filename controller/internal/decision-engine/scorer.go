package decisionengine

// scoreWindow is kept separate so scoring policy can be tested and tuned
// without involving Kubernetes or external signal providers.
func scoreWindow(w Weights, carbonImpact, energy, cost, delay float64) float64 {
	return w.CarbonImpact*carbonImpact +
		w.Energy*energy +
		w.Cost*cost +
		w.Delay*delay
}
