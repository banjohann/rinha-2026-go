package detector

import "testing"

func TestQuantizeDim(t *testing.T) {
	tests := []struct {
		name string
		in   float32
		want uint16
	}{
		{"sentinel", -1, Sentinel},
		{"below zero clamps to sentinel", -0.5, Sentinel},
		{"zero", 0, 0},
		{"half", 0.5, 32767},
		{"one", 1, QuantScale},
		{"above one clamps to scale", 1.5, QuantScale},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := QuantizeDim(tt.in)
			if got != tt.want {
				t.Fatalf("QuantizeDim(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestQuantizeRoundTrip(t *testing.T) {
	cases := []float32{0, 0.1, 0.25, 0.5, 0.75, 1}
	for _, c := range cases {
		q := QuantizeDim(c)
		got := DequantizeDim(q)
		if diff := got - c; diff > 1.0/QuantScale || diff < -1.0/QuantScale {
			t.Fatalf("round-trip %v -> %d -> %v: error too large", c, q, got)
		}
	}
}

func TestDequantizeSentinel(t *testing.T) {
	if got := DequantizeDim(Sentinel); got != -1 {
		t.Fatalf("DequantizeDim(Sentinel) = %v, want -1", got)
	}
}

func TestQuantizeVector(t *testing.T) {
	in := [Dims]float32{0, 0.1667, 0.05, 0.7826, 0.3333, -1, -1, 0.0292, 0.15, 0, 1, 0, 0.15, 0.006}
	q := QuantizeVector(in)
	if q[5] != Sentinel || q[6] != Sentinel {
		t.Fatalf("expected sentinels at 5 and 6, got %d, %d", q[5], q[6])
	}
	if q[10] != QuantScale {
		t.Fatalf("expected card_present=1 to map to %d, got %d", QuantScale, q[10])
	}
	if q[0] != 0 {
		t.Fatalf("expected dim0=0 to map to 0, got %d", q[0])
	}
}
