package config

import (
	"testing"
	"time"
)

func TestLoadObservabilityAndCacheDefaults(t *testing.T) {
	for _, key := range []string{
		"LEDGER_STALL_THRESHOLD",
		"ACCOUNT_METADATA_CACHE_SIZE",
	} {
		t.Setenv(key, "")
	}
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.LedgerStallThreshold != 2*time.Second {
		t.Fatalf("stall threshold = %s, want 2s", configuration.LedgerStallThreshold)
	}
	if configuration.AccountMetadataCacheSize != 100_000 {
		t.Fatalf("cache size = %d, want 100000", configuration.AccountMetadataCacheSize)
	}
}

func TestLoadRejectsUnsafeStallThreshold(t *testing.T) {
	t.Setenv("LEDGER_STALL_THRESHOLD", "10ms")
	if _, err := Load(); err == nil {
		t.Fatal("expected an invalid stall threshold error")
	}
}

func TestLoadAllowsDisablingMetadataCache(t *testing.T) {
	t.Setenv("ACCOUNT_METADATA_CACHE_SIZE", "0")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.AccountMetadataCacheSize != 0 {
		t.Fatalf("cache size = %d, want 0", configuration.AccountMetadataCacheSize)
	}
}
