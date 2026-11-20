package notify

// SLI jalur peringatan Tim Lapangan (P5 kriteria 1 & 8). Ditulis dengan bantuan LLM (Claude).
// Diukur di ujung jalur: dari waktu kejadian (occurred_at, waktu observasi di sumber) sampai
// notifikasi terkirim, jadi mencakup sumber -> aggregator -> Mongo -> outbox -> Kafka -> notifier.

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/core"
)

var (
	metricAlertDelivery = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "bnpb_alert_delivery_seconds",
		Help:    "Waktu dari occurred_at sampai notifikasi Tim Lapangan terkirim.",
		Buckets: []float64{.5, 1, 2, 3, 5, 10, 30, 60, 300},
	}, []string{"hazard_type"})
	metricAlertsSent = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bnpb_alerts_sent_total",
		Help: "Notifikasi Tim Lapangan yang terkirim per jenis bahaya dan severity.",
	}, []string{"hazard_type", "severity"})
)

func init() {
	core.Metrics.MustRegister(metricAlertDelivery, metricAlertsSent)
}
