package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	meilisearch "github.com/meilisearch/meilisearch-go"
	"github.com/statis/wahi-api/internal/api"
	"github.com/statis/wahi-api/internal/config"
	"github.com/statis/wahi-api/internal/ingest"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()

	subcommand := "serve"
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "serve" || args[0] == "ingest" || args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		subcommand = args[0]
		args = args[1:]
	}

	switch subcommand {
	case "serve":
		if err := runServe(cfg, args); err != nil {
			slog.Error("server exited with error", "error", err)
			os.Exit(1)
		}
	case "ingest":
		if err := runIngest(cfg, args); err != nil {
			slog.Error("ingestion failed", "error", err)
			os.Exit(1)
		}
	default:
		printUsage()
	}
}

func runServe(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.String("port", cfg.Port, "HTTP server port")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg.Port = *port

	meiliClient := meilisearch.New(cfg.MeiliHost, meilisearch.WithAPIKey(cfg.MeiliAPIKey))
	srv := api.NewServer(cfg, meiliClient)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	indexer := ingest.NewIndexer(meiliClient, cfg.BatchSize)
	if err := indexer.ConfigureIndexesWithRetry(ctx, time.Second, 30*time.Second); err != nil {
		return fmt.Errorf("synchronize Meilisearch index settings: %w", err)
	}

	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	defer signal.Stop(sighupCh)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighupCh:
				tokens := config.ReloadAccessTokens(".env")
				srv.UpdateAccessTokens(tokens)
				slog.Info("reloaded API access tokens on SIGHUP", "active_tokens", len(tokens))
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting Wāhi API server",
			"addr", httpServer.Addr,
			"meili_host", cfg.MeiliHost,
			"cors_origins", cfg.CORSOrigins,
			"cors_strict", cfg.CORSStrict,
			"auth_enabled", len(cfg.APIAccessTokens) > 0,
			"active_tokens", len(cfg.APIAccessTokens),
			"docs_url", fmt.Sprintf("http://localhost:%s/docs", cfg.Port),
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down Wāhi API server gracefully")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func runIngest(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	suburbsCSV := fs.String("suburbs-csv", cfg.SuburbsCSVPath, "Path to local NZ Suburbs and Localities CSV")
	roadsCSV := fs.String("roads-csv", cfg.RoadsCSVPath, "Path to local NZ Addresses: Road Sections CSV")
	addressesCSV := fs.String("addresses-csv", cfg.AddressesCSVPath, "Path to local NZ Addresses CSV")
	batchSize := fs.Int("batch-size", cfg.BatchSize, "Batch size for streaming address ingestion")
	useFixtures := fs.Bool("fixtures", false, "Ingest sample CSV fixtures from ./testdata")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *useFixtures {
		cfg.SuburbsCSVPath = "testdata/suburbs.csv"
		cfg.RoadsCSVPath = "testdata/roads.csv"
		cfg.AddressesCSVPath = "testdata/addresses.csv"
	} else {
		cfg.SuburbsCSVPath = *suburbsCSV
		cfg.RoadsCSVPath = *roadsCSV
		cfg.AddressesCSVPath = *addressesCSV
	}
	cfg.BatchSize = *batchSize

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	meiliClient := meilisearch.New(cfg.MeiliHost, meilisearch.WithAPIKey(cfg.MeiliAPIKey))
	indexer := ingest.NewIndexer(meiliClient, cfg.BatchSize)
	dl := ingest.NewDownloader(cfg.LINZBaseURL, cfg.LINZAPIKey)

	return indexer.RunPipeline(ctx, cfg, dl)
}

func printUsage() {
	fmt.Println(`Wāhi API - New Zealand Address Autosuggest & Geocoding Server

Usage:
  wahi-api <command> [flags]

Commands:
  serve    Start the HTTP REST API and OpenAPI documentation server (default)
  ingest   Configure Meilisearch indexes and run the LINZ streaming ETL pipeline

Examples:
  wahi-api serve --port 8080
  wahi-api ingest --fixtures
  LINZ_API_KEY=xxx wahi-api ingest`)
}
