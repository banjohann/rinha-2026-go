package detector

import "math"

// Sentinel is the uint16 value reserved for the "no previous transaction"
// marker in dims 5 and 6. The float32 sentinel is -1 (see vector.go).
const Sentinel uint16 = 65535

// QuantScale maps the [0,1] float range to [0, QuantScale]. We use
// 65534 so that 65535 stays available as a sentinel.
const QuantScale = 65534

// QuantizeDim maps a float32 to its uint16 representation. The sentinel
// value (-1) becomes 65535; values in [0,1] map linearly to [0, 65534].
// Out-of-range values are clamped.
func QuantizeDim(v float32) uint16 {
	if v < 0 {
		return Sentinel
	}
	if v >= 1 {
		return QuantScale
	}
	return uint16(math.Round(float64(v) * QuantScale))
}

// QuantizeVector quantizes a 14-dim float vector. Sentinel positions
// produced by Vectorize (-1) round-trip to Sentinel.
func QuantizeVector(v [Dims]float32) [Dims]uint16 {
	var q [Dims]uint16
	for i := 0; i < Dims; i++ {
		q[i] = QuantizeDim(v[i])
	}
	return q
}

// DequantizeDim is the inverse mapping. Sentinel returns -1.
// Useful for tests and diagnostics; not on the hot path.
func DequantizeDim(q uint16) float32 {
	if q == Sentinel {
		return -1
	}
	return float32(q) / float32(QuantScale)
}
