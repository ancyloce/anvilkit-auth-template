package monitoring

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	resultSuccess = "success"
	resultFailure = "failure"
)

type Metrics struct {
	registry      *prometheus.Registry
	sendAttempts  *prometheus.CounterVec
	sendLatency   *prometheus.HistogramVec
	queueBacklog  *prometheus.GaugeVec
	queuePollFail *prometheus.CounterVec
}

func NewMetrics() (*Metrics, error) {
	registry := prometheus.NewRegistry()
	metrics := &Metrics{
		registry: registry,
		sendAttempts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "email_worker_send_attempts_total",
				Help: "Total number of email send attempts partitioned by result.",
			},
			[]string{"result"},
		),
		sendLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "email_worker_send_latency_seconds",
				Help:    "Latency of email send attempts in seconds.",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
			[]string{"result"},
		),
		queueBacklog: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "email_worker_queue_backlog",
				Help: "Current number of queued email jobs waiting to be processed.",
			},
			[]string{"queue"},
		),
		queuePollFail: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "email_worker_queue_backlog_poll_failures_total",
				Help: "Total number of failures while polling queue backlog length.",
			},
			[]string{"queue"},
		),
	}

	if err := registry.Register(collectors.NewGoCollector()); err != nil {
		return nil, err
	}
	if err := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, err
	}
	for _, collector := range []prometheus.Collector{
		metrics.sendAttempts,
		metrics.sendLatency,
		metrics.queueBacklog,
		metrics.queuePollFail,
	} {
		if err := registry.Register(collector); err != nil {
			return nil, err
		}
	}

	return metrics, nil
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveSendSuccess(duration time.Duration) {
	m.observeSend(resultSuccess, duration)
}

func (m *Metrics) ObserveSendFailure(duration time.Duration) {
	m.observeSend(resultFailure, duration)
}

func (m *Metrics) SetQueueBacklog(queueName string, backlog int64) {
	m.queueBacklog.WithLabelValues(queueName).Set(float64(backlog))
}

func (m *Metrics) IncQueueBacklogPollFailure(queueName string) {
	m.queuePollFail.WithLabelValues(queueName).Inc()
}

func (m *Metrics) observeSend(result string, duration time.Duration) {
	m.sendAttempts.WithLabelValues(result).Inc()
	m.sendLatency.WithLabelValues(result).Observe(duration.Seconds())
}
