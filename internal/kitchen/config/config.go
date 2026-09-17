package config

import (
	"fmt"
	"os"
	"time"
)

const (
	defaultHTTPAddress         = ":8080"
	defaultConfirmationTimeout = 2 * time.Minute
	defaultTimeoutScanInterval = 5 * time.Second
	defaultRequestTimeout      = 15 * time.Second
	defaultHealthCheckTimeout  = 2 * time.Second
	defaultShutdownTimeout     = 10 * time.Second
)

type Config struct {
	HTTPAddress         string
	DatabaseURL         string
	ConfirmationTimeout time.Duration
	TimeoutScanInterval time.Duration
	RequestTimeout      time.Duration
	HealthCheckTimeout  time.Duration
	ShutdownTimeout     time.Duration
}

func Load() (Config, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	confirmationTimeout, err := durationFromEnv(
		"CONFIRMATION_TIMEOUT",
		defaultConfirmationTimeout,
	)
	if err != nil {
		return Config{}, err
	}

	timeoutScanInterval, err := durationFromEnv(
		"TIMEOUT_SCAN_INTERVAL",
		defaultTimeoutScanInterval,
	)
	if err != nil {
		return Config{}, err
	}

	requestTimeout, err := durationFromEnv("REQUEST_TIMEOUT", defaultRequestTimeout)
	if err != nil {
		return Config{}, err
	}

	healthCheckTimeout, err := durationFromEnv("HEALTH_CHECK_TIMEOUT", defaultHealthCheckTimeout)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := durationFromEnv("SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}

	httpAddress := os.Getenv("HTTP_ADDRESS")
	if httpAddress == "" {
		httpAddress = defaultHTTPAddress
	}

	return Config{
		HTTPAddress:         httpAddress,
		DatabaseURL:         databaseURL,
		ConfirmationTimeout: confirmationTimeout,
		TimeoutScanInterval: timeoutScanInterval,
		RequestTimeout:      requestTimeout,
		HealthCheckTimeout:  healthCheckTimeout,
		ShutdownTimeout:     shutdownTimeout,
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
