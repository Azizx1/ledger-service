package service

import (
	"testing"

	"github.com/Azizx1/ledger-service/internal/domain"
	tb "github.com/tigerbeetle/tigerbeetle-go"
)

func TestMetadataCacheIsBounded(t *testing.T) {
	t.Parallel()
	cache := newMetadataCache(2)
	first, second, third := tb.ToUint128(10), tb.ToUint128(11), tb.ToUint128(12)
	metadata := accountMetadata{kind: domain.AccountCorporateWallet}

	cache.Store(first, metadata)
	cache.Store(second, metadata)
	cache.Store(third, metadata)

	if cache.Len() != 2 {
		t.Fatalf("cache length = %d, want 2", cache.Len())
	}
	if _, found := cache.Load(first); found {
		t.Fatal("oldest entry was not evicted")
	}
	for _, id := range []tb.Uint128{second, third} {
		if _, found := cache.Load(id); !found {
			t.Fatalf("expected %s to remain cached", id.String())
		}
	}
}

func TestMetadataCacheCanBeDisabled(t *testing.T) {
	t.Parallel()
	cache := newMetadataCache(0)
	cache.Store(tb.ToUint128(10), accountMetadata{kind: domain.AccountCorporateWallet})
	if cache.Len() != 0 {
		t.Fatalf("disabled cache length = %d, want 0", cache.Len())
	}
}
