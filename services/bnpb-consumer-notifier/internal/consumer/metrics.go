package consumer

// Metrik + tracing pemrosesan Kafka (M2 observabilitas). Ditulis dengan bantuan LLM (Claude).
// Consumer tidak melayani HTTP selain /health, jadi throughput, latensi, dan error-nya diukur di
// sini, bukan di metrik HTTP.

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/angkaberapa/AAT/services/bnpb-consumer-notifier/internal/core"
)

var (
	metricRecords = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kafka_consumer_records_total",
		Help: "Record Kafka yang diproses per hasil (ok / error / undecodable).",
	}, []string{"group", "result"})
	metricProcessing = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kafka_consumer_processing_seconds",
		Help:    "Lama memproses satu record (handler).",
		Buckets: []float64{.0005, .001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"group"})
	metricEventAge = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kafka_consumer_event_age_seconds",
		Help:    "Umur record saat diproses: waktu publish ke Kafka sampai selesai diproses consumer.",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300},
	}, []string{"group"})
	metricCommits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kafka_consumer_commits_total",
		Help: "Commit offset per hasil (ok / error). Gagal = record akan dikirim ulang (at-least-once).",
	}, []string{"group", "result"})
)

func init() {
	core.Metrics.MustRegister(metricRecords, metricProcessing, metricEventAge, metricCommits)
}
