package stats

import (
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

const latencyBins = 64

type histogram [latencyBins]uint64

var latencyUpper = func() [latencyBins]float64 {
	var bounds [latencyBins]float64
	for i := range bounds {
		bounds[i] = 10 * math.Pow(1.2, float64(i+1))
	}
	return bounds
}()

func (h *histogram) observe(ms float64) {
	if !math.IsNaN(ms) && !math.IsInf(ms, 0) {
		// shortcut: the end bins absorb outliers, extend the range if real latencies reach them.
		i := sort.Search(latencyBins, func(i int) bool { return ms < latencyUpper[i] })
		h[min(i, latencyBins-1)]++
	}
}

func (h *histogram) encode() []byte {
	b := []byte{}
	for i, n := range h {
		if n != 0 {
			b = append(b, byte(i))
			b = binary.AppendUvarint(b, n)
		}
	}
	return b
}

// Sparse (bin, count) pairs can be concatenated by SQL and merged in one pass.
func (h *histogram) merge(b []byte) error {
	for len(b) != 0 {
		i := b[0]
		n, nbytes := binary.Uvarint(b[1:])
		if i >= latencyBins || nbytes <= 0 {
			return errors.New("invalid request latency histogram")
		}
		h[i] += n
		b = b[1+nbytes:]
	}
	return nil
}

func (h *histogram) percentile(p float64, successes int64) *float64 {
	if successes <= 0 {
		return nil
	}
	rank := uint64(math.Ceil(p*float64(successes) - 1e-9))
	// The old reader ranks all successes and sorts NULLs last in either direction.
	if fromTop := uint64(successes) - rank + 1; rank > fromTop {
		var present uint64
		for _, count := range h {
			present += count
		}
		if fromTop > present {
			return nil
		}
		rank = present - fromTop + 1
	}
	for i, count := range h {
		if rank <= count {
			return new(latencyUpper[i] * (2.0 / 2.2)) // harmonic midpoint: at most 9.1% relative error
		}
		rank -= count
	}
	return nil
}
