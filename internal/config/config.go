package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	Port           string
	MeiliHost      string
	MeiliAPIKey    string
	LINZAPIKey     string
	LINZBaseURL    string
	BatchSize      int
	RateLimitRPS   float64
	RateLimitBurst int
	CORSOrigins    []string

	// Optional local CSV overrides for offline/fixture ingestion
	SuburbsCSVPath   string
	RoadsCSVPath     string
	AddressesCSVPath string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	meiliKey := getEnv("MEILI_API_KEY", "")
	if meiliKey == "" {
		meiliKey = getEnv("MEILI_MASTER_KEY", "change_me_in_production_min_16_bytes")
	}

	corsRaw := getEnv("CORS_ORIGINS", "*")
	var corsOrigins []string
	for _, o := range strings.Split(corsRaw, ",") {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			corsOrigins = append(corsOrigins, trimmed)
		}
	}

	return &Config{
		Port:             getEnv("PORT", "8080"),
		MeiliHost:        getEnv("MEILI_HOST", "http://localhost:7700"),
		MeiliAPIKey:      meiliKey,
		LINZAPIKey:       getEnv("LINZ_API_KEY", ""),
		LINZBaseURL:      getEnv("LINZ_BASE_URL", "https://data.linz.govt.nz"),
		BatchSize:        getEnvInt("INGEST_BATCH_SIZE", 50000),
		RateLimitRPS:     getEnvFloat("RATE_LIMIT_RPS", 50.0),
		RateLimitBurst:   getEnvInt("RATE_LIMIT_BURST", 100),
		CORSOrigins:      corsOrigins,
		SuburbsCSVPath:   getEnv("SUBURBS_CSV_PATH", ""),
		RoadsCSVPath:     getEnv("ROADS_CSV_PATH", ""),
		AddressesCSVPath: getEnv("ADDRESSES_CSV_PATH", ""),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	valStr := getEnv(key, "")
	if valStr == "" {
		return fallback
	}
	val, err := strconv.Atoi(valStr)
	if err != nil || val <= 0 {
		return fallback
	}
	return val
}

func getEnvFloat(key string, fallback float64) float64 {
	valStr := getEnv(key, "")
	if valStr == "" {
		return fallback
	}
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil || val <= 0 {
		return fallback
	}
	return val
}
