package metrics

import (
	"log"
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	initialized atomic.Bool

	reqCounter   *prometheus.CounterVec
	ruleHitCount *prometheus.CounterVec
	latencyHist  *prometheus.HistogramVec
)

// Init sets up the Prometheus collectors and starts an HTTP server on addr if non-empty.
func Init(addr string) {
	if initialized.Load() {
		return
	}
	reqCounter = promauto.NewCounterVec(prometheus.CounterOpts{Name: "naquadah_requests_total", Help: "Total device Handle requests"}, []string{"key"})
	ruleHitCount = promauto.NewCounterVec(prometheus.CounterOpts{Name: "naquadah_rule_hits_total", Help: "Total rule matches"}, []string{"rule"})
	latencyHist = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "naquadah_request_latency_seconds", Help: "Device Handle latency", Buckets: prometheus.DefBuckets}, []string{"key"})
	if addr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		go func() {
			if err := http.ListenAndServe(addr, mux); err != nil {
				log.Printf("metrics server error: %v", err)
			}
		}()
	}
	initialized.Store(true)
}

func IncRequest(key string) {
	if reqCounter != nil {
		reqCounter.WithLabelValues(key).Inc()
	}
}
func ObserveLatency(key string, seconds float64) {
	if latencyHist != nil {
		latencyHist.WithLabelValues(key).Observe(seconds)
	}
}
func RuleHit(name string) {
	if ruleHitCount != nil {
		ruleHitCount.WithLabelValues(name).Inc()
	}
}
