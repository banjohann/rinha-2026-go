package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/johannb/rinha-2026-go/internal/detector"
	"github.com/johannb/rinha-2026-go/internal/server"
)

func main() {
	addr := envDefault("LISTEN_ADDR", ":8000")
	dataDir := envDefault("DATA_DIR", "./data")

	mcc, err := detector.LoadMCCRisk(filepath.Join(dataDir, "mcc_risk.json"))
	if err != nil {
		log.Fatalf("mcc_risk: %v", err)
	}
	norm, err := detector.LoadConstants(filepath.Join(dataDir, "normalization.json"))
	if err != nil {
		log.Fatalf("normalization: %v", err)
	}

	srv := server.New(mcc, norm)

	go func() {
		refsPath := filepath.Join(dataDir, "index.bin")
		log.Printf("loading references from %s", refsPath)
		t0 := time.Now()
		st, err := detector.LoadStoreFromBinary(refsPath)
		if err != nil {
			log.Fatalf("load references: %v", err)
		}
		log.Printf("loaded %d references in %s", st.N, time.Since(t0))
		srv.SetStore(st)
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(ctx, addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func envDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
