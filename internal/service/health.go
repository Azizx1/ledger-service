package service

import (
	"sync"
	"time"

	tb "github.com/tigerbeetle/tigerbeetle-go"
)

type LedgerHealth struct {
	Ready       bool
	InFlight    int64
	Oldest      time.Duration
	LastSuccess time.Time
}

type observedLedger struct {
	ledger         Ledger
	metrics        Metrics
	stallThreshold time.Duration

	mu            sync.Mutex
	nextID        uint64
	active        map[uint64]time.Time
	oldestID      uint64
	oldestStarted time.Time
	lastSuccess   time.Time
}

func newObservedLedger(ledger Ledger, stallThreshold time.Duration, metrics Metrics) *observedLedger {
	if stallThreshold <= 0 {
		stallThreshold = 2 * time.Second
	}
	return &observedLedger{
		ledger:         ledger,
		metrics:        metrics,
		stallThreshold: stallThreshold,
		active:         make(map[uint64]time.Time),
	}
}

func (l *observedLedger) CreateAccount(account tb.Account) (result tb.CreateAccountResult, err error) {
	done := l.begin("create_account")
	defer func() { done(err) }()
	return l.ledger.CreateAccount(account)
}

func (l *observedLedger) CreateTransfer(transfer tb.Transfer) (result tb.CreateTransferResult, err error) {
	done := l.begin("create_transfer")
	defer func() { done(err) }()
	return l.ledger.CreateTransfer(transfer)
}

func (l *observedLedger) LookupAccount(id tb.Uint128) (account tb.Account, found bool, err error) {
	done := l.begin("lookup_account")
	defer func() { done(err) }()
	return l.ledger.LookupAccount(id)
}

func (l *observedLedger) LookupTransfer(id tb.Uint128) (transfer tb.Transfer, found bool, err error) {
	done := l.begin("lookup_transfer")
	defer func() { done(err) }()
	return l.ledger.LookupTransfer(id)
}

func (l *observedLedger) begin(kind string) func(error) {
	started := time.Now()
	l.mu.Lock()
	l.nextID++
	id := l.nextID
	l.active[id] = started
	if l.oldestStarted.IsZero() || started.Before(l.oldestStarted) {
		l.oldestID = id
		l.oldestStarted = started
	}
	if l.metrics != nil {
		l.metrics.SetLedgerCallsInFlight(len(l.active))
	}
	l.mu.Unlock()

	return func(err error) {
		outcome := "success"
		completed := time.Now()

		l.mu.Lock()
		delete(l.active, id)
		if err != nil {
			outcome = "error"
		} else {
			l.lastSuccess = completed
		}
		if id == l.oldestID {
			l.findOldest()
		}
		if l.metrics != nil {
			l.metrics.SetLedgerCallsInFlight(len(l.active))
		}
		l.mu.Unlock()

		if l.metrics != nil {
			l.metrics.ObserveLedgerCall(kind, outcome, completed.Sub(started))
		}
	}
}

func (l *observedLedger) Health() LedgerHealth {
	now := time.Now()
	l.mu.Lock()
	inFlight := len(l.active)
	oldestStarted := l.oldestStarted
	lastSuccess := l.lastSuccess
	l.mu.Unlock()

	oldest := time.Duration(0)
	if !oldestStarted.IsZero() {
		oldest = now.Sub(oldestStarted)
	}
	return LedgerHealth{
		Ready:       oldest < l.stallThreshold,
		InFlight:    int64(inFlight),
		Oldest:      oldest,
		LastSuccess: lastSuccess,
	}
}

func (l *observedLedger) findOldest() {
	l.oldestID = 0
	l.oldestStarted = time.Time{}
	for id, started := range l.active {
		if l.oldestStarted.IsZero() || started.Before(l.oldestStarted) {
			l.oldestID = id
			l.oldestStarted = started
		}
	}
}
