package metrics

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	Submitted *prometheus.CounterVec
	Started   *prometheus.CounterVec
	Succeeded *prometheus.CounterVec
	Failed    *prometheus.CounterVec
	Retried   *prometheus.CounterVec
	DLQ       *prometheus.CounterVec
	Duration  *prometheus.HistogramVec
}

func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Submitted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jetqueue_jobs_submitted_total",
			Help: "Total jobs submitted",
		}, []string{"queue", "type"}),
		Started: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jetqueue_jobs_started_total",
			Help: "Total jobs started",
		}, []string{"queue", "type"}),
		Succeeded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jetqueue_jobs_succeeded_total",
			Help: "Total jobs succeeded",
		}, []string{"queue", "type"}),
		Failed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jetqueue_jobs_failed_total",
			Help: "Total jobs failed",
		}, []string{"queue", "type"}),
		Retried: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jetqueue_jobs_retried_total",
			Help: "Total job retries",
		}, []string{"queue", "type"}),
		DLQ: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jetqueue_jobs_dlq_total",
			Help: "Total jobs sent to DLQ",
		}, []string{"queue", "type"}),
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "jetqueue_job_processing_seconds",
			Help:    "Job processing duration in seconds",
			Buckets: prometheus.DefBuckets,
		}, []string{"queue", "type"}),
	}

	reg.MustRegister(
		m.Submitted,
		m.Started,
		m.Succeeded,
		m.Failed,
		m.Retried,
		m.DLQ,
		m.Duration,
	)

	return m
}
