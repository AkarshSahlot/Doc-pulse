package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config holds all application configuration.
type Config struct {
	// Google Gemini settings
	GeminiAPIKey string
	LLMModel     string

	// GitHub settings
	GitHubWebhookSecret string

	// Server settings
	ServerPort string

	// MappingFile points at the YAML file describing which docs track which code paths.
	MappingFile string
}

// Load reads configuration from environment variables and an optional .env file.
func Load() (*Config, error) {
	// Attempt to load .env file; ignore error if it doesn't exist
	_ = godotenv.Load()

	cfg := &Config{
		GeminiAPIKey:        os.Getenv("GEMINI_API_KEY"),
		LLMModel:            getEnvOrDefault("LLM_MODEL", "gemini-2.5-flash-lite"),
		GitHubWebhookSecret: os.Getenv("GITHUB_WEBHOOK_SECRET"),
		ServerPort:          getEnvOrDefault("SERVER_PORT", "8080"),
		MappingFile:         getEnvOrDefault("DOCPULSE_MAPPING_FILE", "docpulse.yaml"),
	}

	return cfg, nil
}

// MustLoadForCLI loads config and validates that fields required for CLI usage are present.
func MustLoadForCLI() (*Config, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	if cfg.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required; set it in your environment or .env file")
	}

	return cfg, nil
}

// MustLoadForServer loads config and validates that fields required for the webhook server are present.
func MustLoadForServer() (*Config, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	if cfg.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required; set it in your environment or .env file")
	}
	if cfg.GitHubWebhookSecret == "" {
		return nil, fmt.Errorf("GITHUB_WEBHOOK_SECRET is required for the webhook server")
	}

	return cfg, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
