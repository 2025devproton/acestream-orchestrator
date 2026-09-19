package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	StreamRecoveryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "acestream_proxy_stream_recovery_total",
		Help: "Buffer-progress recovery attempts and outcomes (attempted, resumed, failed, exhausted).",
	}, []string{"outcome"})
	StreamRecoveryDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "acestream_proxy_stream_recovery_duration_seconds",
		Help:    "Time from recovery attempt to the first observed complete buffer chunk.",
		Buckets: prometheus.ExponentialBuckets(1, 2, 9),
	})
	HttpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "acestream_proxy_http_requests_total",
		Help: "The total number of HTTP requests processed by the proxy.",
	}, []string{"mode", "endpoint", "status_code"})

	HttpRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "acestream_proxy_http_request_duration_seconds",
		Help:    "Histogram of response latency (seconds) of HTTP requests.",
		Buckets: prometheus.DefBuckets,
	}, []string{"mode", "endpoint"})

	HttpTtfbSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "acestream_proxy_http_ttfb_seconds",
		Help:    "Histogram of Time To First Byte (seconds) of HTTP requests.",
		Buckets: prometheus.DefBuckets,
	}, []string{"mode", "endpoint"})

	ActiveSessions = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "acestream_proxy_active_sessions",
		Help: "The number of active stream sessions.",
	}, []string{"mode"})

	BytesIngressTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "acestream_proxy_bytes_ingress_total",
		Help: "The total number of bytes received from engines.",
	}, []string{"mode"})

	BytesEgressTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "acestream_proxy_bytes_egress_total",
		Help: "The total number of bytes sent to clients.",
	}, []string{"mode"})

	ConnectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "acestream_proxy_connections_total",
		Help: "The total number of client connections established.",
	}, []string{"mode"})
)
