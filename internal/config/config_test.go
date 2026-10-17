package config

import (
	"os"
	"path/filepath"
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
	if cfg.CORSStrict {
		t.Errorf("expected default CORSStrict false, got true")
	}
	if len(cfg.APIAccessTokens) != 0 {
		t.Errorf("expected empty default APIAccessTokens, got %v", cfg.APIAccessTokens)
	}
}

func TestLoadCustomEnv(t *testing.T) {
	os.Clearenv()
	t.Setenv("PORT", "9090")
	t.Setenv("MEILI_HOST", "http://meilisearch:7700")
	t.Setenv("MEILI_MASTER_KEY", "master_secret_key_123")
	t.Setenv("LINZ_API_KEY", "linz_key_abc")
	t.Setenv("INGEST_BATCH_SIZE", "10000")
	t.Setenv("CORS_ORIGINS", "https://example.com, https://*.example.com")
	t.Setenv("CORS_STRICT", "true")
	t.Setenv("API_ACCESS_TOKENS", "tok_current_v2, tok_previous_v1")
	t.Setenv("API_ACCESS_TOKEN", "tok_legacy_single")

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
	if !cfg.CORSStrict {
		t.Errorf("expected CORSStrict true")
	}
	if len(cfg.APIAccessTokens) != 3 || cfg.APIAccessTokens[0] != "tok_current_v2" || cfg.APIAccessTokens[1] != "tok_previous_v1" || cfg.APIAccessTokens[2] != "tok_legacy_single" {
		t.Errorf("unexpected APIAccessTokens: %v", cfg.APIAccessTokens)
	}
}

func TestReloadAccessTokensFromDotEnv(t *testing.T) {
	os.Clearenv()
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	if err := os.WriteFile(envPath, []byte("API_ACCESS_TOKENS=token_v1\n"), 0600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	tokens := ReloadAccessTokens(envPath)
	if len(tokens) != 1 || tokens[0] != "token_v1" {
		t.Fatalf("expected [token_v1], got %v", tokens)
	}

	// Simulate rotating token in .env to include v2 and v1
	if err := os.WriteFile(envPath, []byte("API_ACCESS_TOKENS=token_v2,token_v1\n"), 0600); err != nil {
		t.Fatalf("write rotated .env: %v", err)
	}
	rotated := ReloadAccessTokens(envPath)
	if len(rotated) != 2 || rotated[0] != "token_v2" || rotated[1] != "token_v1" {
		t.Fatalf("expected [token_v2 token_v1], got %v", rotated)
	}
}
