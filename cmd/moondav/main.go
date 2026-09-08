package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/3115a083/MoonDav/internal/moondav"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:8765/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
		_ = resp.Body.Close()
		return
	}

	cfg, err := moondav.LoadConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	app, err := moondav.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	log.Printf("MoonDav listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server error: %v", err)
		os.Exit(1)
	}
}
