// Package leader memilih satu instance aggregator yang boleh polling (P2 M2).
//
// Pemilihan memakai Lease Kubernetes (coordination.k8s.io) yang disimpan di etcd control plane.
// Kedaluwarsa lease dinilai tiap kandidat dengan jam MONOTONIK lokalnya sendiri: "sudah berapa
// lama sejak saya terakhir melihat lease ini diperbarui", bukan membandingkan renewTime (jam
// dinding pemegang) dengan jam dinding sendiri. Jadi jam node yang bergeser tidak membuat dua
// instance sama-sama merasa leader. Sisa celahnya (leader yang freeze lalu bangun dan belum sadar
// lease-nya hilang) ditutup oleh fencing token di storage (store.ErrFenced).
package leader

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

type Config struct {
	Enabled       bool // false = selalu leader (docker compose / go run tanpa cluster)
	LeaseName     string
	Namespace     string
	Identity      string // nama pod
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

// Term dijalankan selama instance ini leader. ctx-nya dibatalkan saat lease hilang; Term harus
// segera kembali. Bila Term kembali lebih dulu (mis. tulisan di-fence), lease dilepas dan
// instance ikut pemilihan lagi.
type Term func(ctx context.Context)

// Run mengikuti pemilihan leader berulang kali sampai ctx dibatalkan.
func Run(ctx context.Context, cfg Config, logger *slog.Logger, term Term) error {
	logger = logger.With("component", "leader", "identity", cfg.Identity)
	if !cfg.Enabled {
		logger.Warn("leader election disabled: this instance always acts as leader")
		for ctx.Err() == nil {
			term(ctx)
			sleep(ctx, cfg.RetryPeriod)
		}
		return nil
	}

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("kubernetes in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	lock := &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Name: cfg.LeaseName, Namespace: cfg.Namespace},
		Client:     client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: cfg.Identity},
	}

	for ctx.Err() == nil {
		runCtx, cancel := context.WithCancel(ctx)
		elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
			Lock:            lock,
			Name:            cfg.LeaseName,
			LeaseDuration:   cfg.LeaseDuration,
			RenewDeadline:   cfg.RenewDeadline,
			RetryPeriod:     cfg.RetryPeriod,
			ReleaseOnCancel: true, // SIGTERM / rolling update: serahkan lease segera, tanpa menunggu kedaluwarsa
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(leadCtx context.Context) {
					logger.Info("leadership acquired")
					term(leadCtx)
					// Term selesai sendiri (mis. di-fence): lepaskan lease dan ikut pemilihan ulang.
					cancel()
				},
				OnStoppedLeading: func() {
					logger.Info("leadership lost")
				},
				OnNewLeader: func(identity string) {
					if identity != cfg.Identity {
						logger.Info("following leader", "leader", identity)
					}
				},
			},
		})
		if err != nil {
			cancel()
			return fmt.Errorf("leader elector: %w", err)
		}
		elector.Run(runCtx)
		cancel()
		sleep(ctx, cfg.RetryPeriod)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
