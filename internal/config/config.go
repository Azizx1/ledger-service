package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tb "github.com/tigerbeetle/tigerbeetle-go"
)

type Config struct {
	HTTPAddress               string
	TigerBeetleClusterID      tb.Uint128
	TigerBeetleAddresses      []string
	LedgerID                  uint32
	AuthorizationTimeout      time.Duration
	RiskEvaluationDelay       time.Duration
	RiskAutoApproveLimitCents uint64
	MaxConcurrentRequests     int
}

func Load() (Config, error) {
	clusterID, err := tb.HexStringToUint128(env("TB_CLUSTER_ID", "0"))
	if err != nil {
		return Config{}, fmt.Errorf("TB_CLUSTER_ID: %w", err)
	}

	ledgerID, err := uint32Env("TB_LEDGER_ID", 1)
	if err != nil || ledgerID == 0 {
		return Config{}, fmt.Errorf("TB_LEDGER_ID must be a non-zero uint32")
	}

	authorizationTimeout, err := durationEnv("AUTHORIZATION_TIMEOUT", time.Hour)
	if err != nil || authorizationTimeout < time.Second || authorizationTimeout > time.Duration(^uint32(0))*time.Second {
		return Config{}, fmt.Errorf("AUTHORIZATION_TIMEOUT must be between 1s and %ds", uint64(^uint32(0)))
	}

	riskDelay, err := durationEnv("RISK_EVALUATION_DELAY", 200*time.Millisecond)
	if err != nil || riskDelay < 0 {
		return Config{}, fmt.Errorf("RISK_EVALUATION_DELAY must be a non-negative duration")
	}

	riskLimit, err := uint64Env("RISK_AUTO_APPROVE_LIMIT_CENTS", 100_000)
	if err != nil || riskLimit == 0 {
		return Config{}, fmt.Errorf("RISK_AUTO_APPROVE_LIMIT_CENTS must be a positive uint64")
	}

	maxConcurrentRequests, err := intEnv("MAX_CONCURRENT_REQUESTS", 4096)
	if err != nil || maxConcurrentRequests <= 0 || maxConcurrentRequests > 1_000_000 {
		return Config{}, fmt.Errorf("MAX_CONCURRENT_REQUESTS must be between 1 and 1000000")
	}

	addresses := strings.Split(env("TB_ADDRESSES", "127.0.0.1:3000"), ",")
	for i := range addresses {
		addresses[i] = strings.TrimSpace(addresses[i])
		if addresses[i] == "" {
			return Config{}, fmt.Errorf("TB_ADDRESSES contains an empty address")
		}
	}

	return Config{
		HTTPAddress:               env("HTTP_ADDRESS", ":8080"),
		TigerBeetleClusterID:      clusterID,
		TigerBeetleAddresses:      addresses,
		LedgerID:                  ledgerID,
		AuthorizationTimeout:      authorizationTimeout,
		RiskEvaluationDelay:       riskDelay,
		RiskAutoApproveLimitCents: riskLimit,
		MaxConcurrentRequests:     maxConcurrentRequests,
	}, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	return time.ParseDuration(value)
}

func uint32Env(key string, fallback uint32) (uint32, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	return uint32(parsed), err
}

func uint64Env(key string, fallback uint64) (uint64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func intEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}
