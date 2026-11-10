package carbon

import (
	"math"
)

func validIntensity(value float64) bool {
	return !math.IsNaN(value) &&
		!math.IsInf(value, 0) &&
		value >= 0
}
