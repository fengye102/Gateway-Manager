package main

import (
	"sort"
	"sync"
)

var defaultBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 180}

type histogram struct {
	mu      sync.Mutex
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

func newHistogram() *histogram {
	buckets := append([]float64(nil), defaultBuckets...)
	return &histogram{buckets: buckets, counts: make([]uint64, len(buckets))}
}

func (h *histogram) observe(v float64) {
	if v < 0 {
		v = 0
	}
	h.mu.Lock()
	for i, bound := range h.buckets {
		if v <= bound {
			h.counts[i]++
		}
	}
	h.sum += v
	h.count++
	h.mu.Unlock()
}

type histogramSnapshot struct {
	Buckets []float64
	Counts  []uint64
	Sum     float64
	Count   uint64
}

func (h *histogram) snapshot() histogramSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return histogramSnapshot{
		Buckets: append([]float64(nil), h.buckets...),
		Counts:  append([]uint64(nil), h.counts...),
		Sum:     h.sum,
		Count:   h.count,
	}
}

func (m *metricWriter) histogramFamily(name string, help string, labels [][2]string, snap histogramSnapshot) {
	m.header(name, help, "histogram")
	for i, bound := range snap.Buckets {
		lb := append(append([][2]string(nil), labels...), [2]string{"le", formatFloat(bound)})
		m.sample(name+"_bucket", lb, uintStr(snap.Counts[i]))
	}
	infLabels := append(append([][2]string(nil), labels...), [2]string{"le", "+Inf"})
	m.sample(name+"_bucket", infLabels, uintStr(snap.Count))
	m.sample(name+"_sum", labels, formatFloat(snap.Sum))
	m.sample(name+"_count", labels, uintStr(snap.Count))
}

func uintStr(v uint64) string {
	return formatUint(v)
}

func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	var digits [20]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	buf = append(buf, digits[i:]...)
	return string(buf)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
