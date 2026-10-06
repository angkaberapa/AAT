package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/angkaberapa/AAT/services/bnpb-auth/internal/config"
	"github.com/angkaberapa/AAT/services/bnpb-auth/internal/core"
	"github.com/angkaberapa/AAT/services/bnpb-auth/internal/token"
)

func main() {
	core.RunHealthcheckIfRequested()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration:\n%v\n", err)
		os.Exit(1)
	}
	logger := core.NewLogger(cfg.ServiceName, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	mux := core.NewMux(cfg.ServiceName)
	tokenService, err := token.NewService(token.Config{
		SigningSecret:  cfg.JWTSigningKey,
		Issuer:         cfg.JWTIssuer,
		Audience:       cfg.JWTAudience,
		AccessTokenTTL: cfg.AccessTokenTTL,
		RefreshTTL:     cfg.RefreshTokenTTL,
		Clients: []token.Client{
			{ID: cfg.ClientMediaID, Secret: cfg.ClientMediaSecret, Scope: token.ScopeHazardsSummary},
			{ID: cfg.ClientFieldID, Secret: cfg.ClientFieldSecret, Scope: token.ScopeHazardsRead},
			{ID: cfg.ClientOpsID, Secret: cfg.ClientOpsSecret, Scope: token.ScopeHazardsRead},
		},
	})
	if err != nil {
		logger.Error("initialize token service", "error", err)
		os.Exit(1)
	}
	token.NewHandler(tokenService).Register(mux)

	if err := core.Serve(ctx, logger, cfg.Port, mux); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
