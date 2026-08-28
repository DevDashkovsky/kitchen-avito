package config

import (
	"fmt"
	"os"
	"time"
)

const (
	defaultKitchenBaseURL  = "http://localhost:8080"
	defaultProcessInterval = 5 * time.Second
	defaultRequestTimeout  = 10 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

type Config struct {
	KitchenBaseURL  string
	PartnerAPIKey   string
	ProcessInterval time.Duration
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
}

func Load() (Config, error) {
	partnerAPIKey := os.Getenv("PARTNER_API_KEY")
	if partnerAPIKey == "" {
		return Config{}, fmt.Errorf("PARTNER_API_KEY is required")
	}

	processInterval, err := durationFromEnv("PROCESS_INTERVAL", defaultProcessInterval)
	if err != nil {
		return Config{}, err
	}

	requestTimeout, err := durationFromEnv("REQUEST_TIMEOUT", defaultRequestTimeout)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := durationFromEnv("SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}

	kitchenBaseURL := os.Getenv("KITCHEN_BASE_URL")
	if kitchenBaseURL == "" {
		kitchenBaseURL = defaultKitchenBaseURL
	}

	return Config{
		KitchenBaseURL:  kitchenBaseURL,
		PartnerAPIKey:   partnerAPIKey,
		ProcessInterval: processInterval,
		RequestTimeout:  requestTimeout,
		ShutdownTimeout: shutdownTimeout,
	}, nil
}

func durationFromEnv(name string, defaultValue time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return defaultValue, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}

	return duration, nil
}
