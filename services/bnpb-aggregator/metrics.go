package main

// Metrik P2 untuk dashboard (M2 observabilitas). Ditulis dengan bantuan LLM (Claude).
// Disajikan di GET /metrics lewat core.Metrics; label pod/instance ditambahkan Prometheus.

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/ingest"
)

var (
	metricIsLeader = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bnpb_aggregator_is_leader",
		Help: "1 bila instance ini sedang memegang Lease ingest (polling + relay outbox).",
	})
	metricLeaderEpoch = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "bnpb_aggregator_leader_epoch",
		Help: "Fencing token (epoch) masa jabatan leader terakhir instance ini.",
	})
	metricLeaderTerms = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "bnpb_aggregator_leader_terms_total",
		Help: "Berapa kali instance ini menjadi leader.",
	})
	metricFencedWrites = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "bnpb_aggregator_fenced_writes_total",
		Help: "Tulisan yang ditolak storage karena epoch sudah basi (leader lama).",
	})
	metricEventsStored = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bnpb_aggregator_events_stored_total",
		Help: "HazardEvent hasil poll per hasil simpan (inserted / updated / unchanged).",
	}, []string{"result"})
)

// sourceStatusCollector melaporkan status kebaruan tiap sumber saat di-scrape:
// bnpb_source_status{source, status} = 1 untuk status saat ini.
type sourceStatusCollector struct {
	statuses *ingest.SharedStatus
	desc     *prometheus.Desc
}

func (c sourceStatusCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c sourceStatusCollector) Collect(ch chan<- prometheus.Metric) {
	for _, st := range c.statuses.Snapshot() {
		for _, s := range []string{ingest.StatusOK, ingest.StatusStale, ingest.StatusUnavailable, ingest.StatusUnknown} {
			v := 0.0
			if st.Status == s {
				v = 1
			}
			ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, v, string(st.Source), s)
		}
	}
}

func registerMetrics(statuses *ingest.SharedStatus) {
	core.Metrics.MustRegister(metricIsLeader, metricLeaderEpoch, metricLeaderTerms,
		metricFencedWrites, metricEventsStored,
		sourceStatusCollector{statuses: statuses, desc: prometheus.NewDesc(
			"bnpb_source_status", "Status kebaruan data per sumber (1 = status saat ini).",
			[]string{"source", "status"}, nil)})
}
