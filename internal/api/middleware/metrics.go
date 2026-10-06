package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devrishijain/table-manager/internal/storage"
)

// EndpointMetric tracks performance and egress for an individual API route.
type EndpointMetric struct {
	Method         string           `json:"method"`
	Pattern        string           `json:"pattern"`
	Count          int64            `json:"count"`
	TotalBytes     int64            `json:"total_bytes"`
	TotalDBQueries int64            `json:"total_db_queries"`
	MinDurationMs  float64          `json:"min_duration_ms"`
	MaxDurationMs  float64          `json:"max_duration_ms"`
	MeanDurationMs float64          `json:"mean_duration_ms"`
	P50DurationMs  float64          `json:"p50_duration_ms"`
	P90DurationMs  float64          `json:"p90_duration_ms"`
	P95DurationMs  float64          `json:"p95_duration_ms"`
	P99DurationMs  float64          `json:"p99_duration_ms"`
	StatusCodes    map[int]int64    `json:"status_codes"`
	durationsMs    []float64
}

// MetricsCollector is the global thread-safe registry of HTTP request metrics.
type MetricsCollector struct {
	mu          sync.RWMutex
	startTime   time.Time
	totalReqs   int64
	totalEgress int64
	endpoints   map[string]*EndpointMetric
}

var GlobalMetrics = NewMetricsCollector()

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		startTime: time.Now(),
		endpoints: make(map[string]*EndpointMetric),
	}
}

func (m *MetricsCollector) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startTime = time.Now()
	m.totalReqs = 0
	m.totalEgress = 0
	m.endpoints = make(map[string]*EndpointMetric)
}

func (m *MetricsCollector) Record(method, pattern string, status int, bytesWritten int64, duration time.Duration, dbQueries int64) {
	key := method + " " + pattern
	durationMs := float64(duration.Microseconds()) / 1000.0

	atomic.AddInt64(&m.totalReqs, 1)
	atomic.AddInt64(&m.totalEgress, bytesWritten)

	m.mu.Lock()
	defer m.mu.Unlock()

	em, exists := m.endpoints[key]
	if !exists {
		em = &EndpointMetric{
			Method:        method,
			Pattern:       pattern,
			StatusCodes:   make(map[int]int64),
			MinDurationMs: durationMs,
			MaxDurationMs: durationMs,
			durationsMs:   make([]float64, 0, 128),
		}
		m.endpoints[key] = em
	}

	em.Count++
	em.TotalBytes += bytesWritten
	em.TotalDBQueries += dbQueries
	em.StatusCodes[status]++
	em.durationsMs = append(em.durationsMs, durationMs)

	if durationMs < em.MinDurationMs {
		em.MinDurationMs = durationMs
	}
	if durationMs > em.MaxDurationMs {
		em.MaxDurationMs = durationMs
	}
}

type MetricsSnapshot struct {
	UptimeSeconds    float64          `json:"uptime_seconds"`
	TotalRequests    int64            `json:"total_requests"`
	TotalEgressBytes int64            `json:"total_egress_bytes"`
	TotalEgressMB    float64          `json:"total_egress_mb"`
	Endpoints        []EndpointMetric `json:"endpoints"`
}

func (m *MetricsCollector) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	uptime := time.Since(m.startTime).Seconds()
	snapshot := MetricsSnapshot{
		UptimeSeconds:    uptime,
		TotalRequests:    atomic.LoadInt64(&m.totalReqs),
		TotalEgressBytes: atomic.LoadInt64(&m.totalEgress),
		TotalEgressMB:    float64(atomic.LoadInt64(&m.totalEgress)) / (1024 * 1024),
		Endpoints:        make([]EndpointMetric, 0, len(m.endpoints)),
	}

	for _, em := range m.endpoints {
		metricCopy := *em
		metricCopy.StatusCodes = make(map[int]int64)
		for k, v := range em.StatusCodes {
			metricCopy.StatusCodes[k] = v
		}

		if len(em.durationsMs) > 0 {
			sorted := make([]float64, len(em.durationsMs))
			copy(sorted, em.durationsMs)
			sort.Float64s(sorted)

			var sum float64
			for _, d := range sorted {
				sum += d
			}
			metricCopy.MeanDurationMs = sum / float64(len(sorted))
			metricCopy.P50DurationMs = percentile(sorted, 0.50)
			metricCopy.P90DurationMs = percentile(sorted, 0.90)
			metricCopy.P95DurationMs = percentile(sorted, 0.95)
			metricCopy.P99DurationMs = percentile(sorted, 0.99)
		}

		snapshot.Endpoints = append(snapshot.Endpoints, metricCopy)
	}

	sort.Slice(snapshot.Endpoints, func(i, j int) bool {
		return snapshot.Endpoints[i].Count > snapshot.Endpoints[j].Count
	})

	return snapshot
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

type metricsResponseRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (r *metricsResponseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *metricsResponseRecorder) Write(b []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytesWritten += int64(n)
	return n, err
}

func (r *metricsResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// RequestMetricsMiddleware measures per-request latency, egress, and DB queries.
func RequestMetricsMiddleware(collector *MetricsCollector) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			counter := &storage.QueryCounter{}
			ctx := context.WithValue(r.Context(), storage.QueryCounterKey, counter)

			rec := &metricsResponseRecorder{
				ResponseWriter: w,
				statusCode:     0,
			}

			next.ServeHTTP(rec, r.WithContext(ctx))

			status := rec.statusCode
			if status == 0 {
				status = http.StatusOK
			}

			duration := time.Since(start)
			pattern := cleanRoutePath(r.URL.Path)

			collector.Record(r.Method, pattern, status, rec.bytesWritten, duration, counter.Value())
		})
	}
}

func cleanRoutePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if len(p) >= 32 || (len(p) == 36 && strings.Count(p, "-") == 4) {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}

// MetricsHandler exposes the live metrics JSON snapshot.
func MetricsHandler(collector *MetricsCollector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(collector.Snapshot())
	}
}
