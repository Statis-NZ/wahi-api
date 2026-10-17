package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	os.Clearenv()

	cfg := Load()
	if cfg.Port != "8080" {
		t.Errorf("expected default Port 8080, got %s", cfg.Port)
	}
	if cfg.MeiliHost != "http://localhost:7700" {
		t.Errorf("expected default MeiliHost http://localhost:7700, got %s", cfg.MeiliHost)
	}
	if cfg.BatchSize != 50000 {
		t.Errorf("expected default BatchSize 50000, got %d", cfg.BatchSize)
	}
	if cfg.MeiliAPIKey != "change_me_in_production_min_16_bytes" {
		t.Errorf("unexpected default MeiliAPIKey: %s", cfg.MeiliAPIKey)
	}
}

func TestLoadCustomEnv(t *testing.T) {
	os.Clearenv()
	t.Setenv("PORT", "9090")
	t.Setenv("MEILI_HOST", "http://meilisearch:7700")
	t.Setenv("MEILI_MASTER_KEY", "master_secret_key_123")
	t.Setenv("LINZ_API_KEY", "linz_key_abc")
	t.Setenv("INGEST_BATCH_SIZE", "10000")
	t.Setenv("CORS_ORIGINS", "https://example.com, https://app.example.com")

	cfg := Load()
	if cfg.Port != "9090" {
		t.Errorf("expected Port 9090, got %s", cfg.Port)
	}
	if cfg.MeiliHost != "http://meilisearch:7700" {
		t.Errorf("expected MeiliHost http://meilisearch:7700, got %s", cfg.MeiliHost)
	}
	if cfg.MeiliAPIKey != "master_secret_key_123" {
		t.Errorf("expected MeiliAPIKey master_secret_key_123, got %s", cfg.MeiliAPIKey)
	}
	if cfg.LINZAPIKey != "linz_key_abc" {
		t.Errorf("expected LINZAPIKey linz_key_abc, got %s", cfg.LINZAPIKey)
	}
	if cfg.BatchSize != 10000 {
		t.Errorf("expected BatchSize 10000, got %d", cfg.BatchSize)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://example.com" {
		t.Errorf("unexpected CORSOrigins: %v", cfg.CORSOrigins)
	}
}
