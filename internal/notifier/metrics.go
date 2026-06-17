package notifier

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	messagesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notify_messages_total",
			Help: "Notification messages processed, by subject and outcome (ack/nak/term).",
		},
		[]string{"subject", "outcome"},
	)
	sendDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "notify_send_duration_seconds",
			Help:    "Successful SMTP send latency in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)
)
