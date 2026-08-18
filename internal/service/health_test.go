package service

import (
	"fmt"
	"testing"
	"time"

	tb "github.com/tigerbeetle/tigerbeetle-go"
)

func TestObservedLedgerReportsAndRecoversFromAStall(t *testing.T) {
	t.Parallel()
	ledger := &blockingLedger{entered: make(chan struct{}), release: make(chan struct{})}
	observed := newObservedLedger(ledger, 20*time.Millisecond, nil)
	done := make(chan error, 1)
	go func() {
		_, err := observed.CreateTransfer(tb.Transfer{})
		done <- err
	}()
	<-ledger.entered

	deadline := time.Now().Add(time.Second)
	for observed.Health().Ready && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	health := observed.Health()
	if health.Ready || health.InFlight != 1 || health.Oldest < 20*time.Millisecond {
		t.Fatalf("unexpected stalled health: %+v", health)
	}

	close(ledger.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	health = observed.Health()
	if !health.Ready || health.InFlight != 0 || health.Oldest != 0 || health.LastSuccess.IsZero() {
		t.Fatalf("unexpected recovered health: %+v", health)
	}
}

type blockingLedger struct {
	entered chan struct{}
	release chan struct{}
}

func (l *blockingLedger) CreateAccount(tb.Account) (tb.CreateAccountResult, error) {
	return tb.CreateAccountResult{}, fmt.Errorf("unexpected call")
}

func (l *blockingLedger) CreateTransfer(tb.Transfer) (tb.CreateTransferResult, error) {
	close(l.entered)
	<-l.release
	return tb.CreateTransferResult{Status: tb.TransferCreated}, nil
}

func (l *blockingLedger) LookupAccount(tb.Uint128) (tb.Account, bool, error) {
	return tb.Account{}, false, fmt.Errorf("unexpected call")
}

func (l *blockingLedger) LookupTransfer(tb.Uint128) (tb.Transfer, bool, error) {
	return tb.Transfer{}, false, fmt.Errorf("unexpected call")
}
