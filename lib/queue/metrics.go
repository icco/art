package queue

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	jobsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "art_jobs_processed_total",
		Help: "Job outcomes by kind; status pending means a retry was scheduled.",
	}, []string{"kind", "status"})
	jobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "art_job_duration_seconds",
		Help:    "Job execution time by kind.",
		Buckets: prometheus.ExponentialBuckets(0.1, 4, 8),
	}, []string{"kind"})
)
