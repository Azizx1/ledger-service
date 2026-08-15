package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Azizx1/ledger-service/internal/domain"
	"github.com/Azizx1/ledger-service/internal/ledger"
	tb "github.com/tigerbeetle/tigerbeetle-go"
)

const (
	testCorporateAccountID = "10000000000000000000000000000001"
	testCardAccountID      = "10000000000000000000000000000002"
	testOtherCorporateID   = "10000000000000000000000000000003"
	testMerchantID         = "MRC_009"
)

func TestCreateAccountUsesCallerTigerBeetleIDIdempotently(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	parentID := testID()
	createTestAccount(t, ledgerService, domain.AccountCorporateWallet, parentID, "")
	accountID := testID()
	request := domain.CreateAccountRequest{AccountID: accountID, Kind: domain.AccountCorporateWallet}

	created, status, err := ledgerService.CreateAccount(request)
	if err != nil || status != 201 || created.Status != "created" || created.AccountID != accountID {
		t.Fatalf("create account: status=%d response=%+v err=%v", status, created, err)
	}
	storedID, err := tb.HexStringToUint128(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := ledgerClient.accounts[storedID.String()]; !found {
		t.Fatal("caller-provided ID was not used as TigerBeetle Account.id")
	}

	existing, status, err := ledgerService.CreateAccount(request)
	if err != nil || status != 200 || existing.Status != "exists" {
		t.Fatalf("recreate account: status=%d response=%+v err=%v", status, existing, err)
	}

	request.Kind = domain.AccountCardWallet
	request.CorporateAccountID = parentID
	if _, _, err := ledgerService.CreateAccount(request); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected account idempotency conflict, got %v", err)
	}
}

func TestReservedTigerBeetleIDsAreRejectedAtTheAPIBoundary(t *testing.T) {
	t.Parallel()
	ledgerService := newTestService(t, newFakeLedger())
	for _, accountID := range []string{"0", "ffffffffffffffffffffffffffffffff"} {
		_, _, err := ledgerService.CreateAccount(domain.CreateAccountRequest{
			AccountID: accountID,
			Kind:      domain.AccountCorporateWallet,
		})
		if !errors.Is(err, domain.ErrInvalidRequest) {
			t.Fatalf("expected reserved ID %s to be rejected, got %v", accountID, err)
		}
	}
}

func TestColdCacheRejectsAccountWithoutRequiredInvariant(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	accountID, err := tb.HexStringToUint128(testCorporateAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerClient.CreateAccount(tb.Account{
		ID: accountID, Ledger: 1, Code: ledger.AccountCodeCorporateWallet,
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = ledgerService.TopUp(domain.TopUpRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, AmountCents: 100,
	})
	if !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("expected unsafe out-of-band account to be rejected, got %v", err)
	}
}

func TestTopUpIsExactlyOnceAcrossAmbiguousFailure(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerClient.failAfterCommit = true
	ledgerService := newTestService(t, ledgerClient)
	createTestAccount(t, ledgerService, domain.AccountCorporateWallet, testCorporateAccountID, "")

	request := domain.TopUpRequest{RequestID: testID(), AccountID: testCorporateAccountID, AmountCents: 1_000}
	if _, _, err := ledgerService.TopUp(request); err == nil {
		t.Fatal("expected simulated ambiguous failure")
	}
	response, status, err := ledgerService.TopUp(request)
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 || response.Status != "posted" || response.LedgerStatus != tb.TransferExists.String() {
		t.Fatalf("unexpected replay response: status=%d response=%+v", status, response)
	}
	if len(ledgerClient.transfers) != 1 {
		t.Fatalf("expected one economic transfer, got %d", len(ledgerClient.transfers))
	}

	balance, err := ledgerService.Balance(testCorporateAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if balance.PostedBalance != "1000" || balance.AvailableBalance != "1000" {
		t.Fatalf("unexpected balance: %+v", balance)
	}
}

func TestRequestIDPayloadConflict(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	createTestAccount(t, ledgerService, domain.AccountCorporateWallet, testCorporateAccountID, "")

	first := domain.TopUpRequest{RequestID: testID(), AccountID: testCorporateAccountID, AmountCents: 100}
	if _, _, err := ledgerService.TopUp(first); err != nil {
		t.Fatal(err)
	}
	first.AmountCents = 200
	if _, _, err := ledgerService.TopUp(first); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestFailedTransferIDCannotLaterSucceed(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	createTestAccount(t, ledgerService, domain.AccountCorporateWallet, testCorporateAccountID, "")

	request := domain.WithdrawalRequest{RequestID: testID(), AccountID: testCorporateAccountID, AmountCents: 100}
	first, status, err := ledgerService.Withdraw(request)
	if err != nil || status != 422 || first.ErrorCode != "insufficient_funds" {
		t.Fatalf("first withdrawal: status=%d response=%+v err=%v", status, first, err)
	}
	if _, _, err := ledgerService.TopUp(domain.TopUpRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, AmountCents: 1_000,
	}); err != nil {
		t.Fatal(err)
	}

	retry, status, err := ledgerService.Withdraw(request)
	if err != nil || status != 409 || retry.ErrorCode != "previous_attempt_failed" {
		t.Fatalf("retried withdrawal: status=%d response=%+v err=%v", status, retry, err)
	}
	balance, err := ledgerService.Balance(testCorporateAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if balance.PostedBalance != "1000" {
		t.Fatalf("failed transfer was applied on retry: %+v", balance)
	}
}

func TestCardAllocationAndReturnMoveFundsWithoutDuplication(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	createCorporateAndCard(t, ledgerService)
	if _, _, err := ledgerService.TopUp(domain.TopUpRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, AmountCents: 1_000,
	}); err != nil {
		t.Fatal(err)
	}

	allocation, status, err := ledgerService.AllocateToCard(domain.CardAllocationRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, CardID: testCardAccountID, AmountCents: 700,
	})
	if err != nil || status != 200 || allocation.Status != "posted" {
		t.Fatalf("allocation: status=%d response=%+v err=%v", status, allocation, err)
	}
	assertBalance(t, ledgerService, testCorporateAccountID, "300", "300")
	cardBalance := assertBalance(t, ledgerService, testCardAccountID, "700", "700")
	if cardBalance.CorporateAccountID != testCorporateAccountID {
		t.Fatalf("card parent not retained: %+v", cardBalance)
	}
	overAllocation, status, err := ledgerService.AllocateToCard(domain.CardAllocationRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, CardID: testCardAccountID, AmountCents: 400,
	})
	if err != nil || status != 422 || overAllocation.ErrorCode != "insufficient_funds" {
		t.Fatalf("over-allocation: status=%d response=%+v err=%v", status, overAllocation, err)
	}

	returned, status, err := ledgerService.ReturnFromCard(domain.CardReturnRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, CardID: testCardAccountID, AmountCents: 200,
	})
	if err != nil || status != 200 || returned.Status != "posted" {
		t.Fatalf("return: status=%d response=%+v err=%v", status, returned, err)
	}
	assertBalance(t, ledgerService, testCorporateAccountID, "500", "500")
	assertBalance(t, ledgerService, testCardAccountID, "500", "500")
}

func TestCardAllocationRejectsWrongCorporateParent(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	createCorporateAndCard(t, ledgerService)
	createTestAccount(t, ledgerService, domain.AccountCorporateWallet, testOtherCorporateID, "")

	_, _, err := ledgerService.AllocateToCard(domain.CardAllocationRequest{
		RequestID: testID(), AccountID: testOtherCorporateID, CardID: testCardAccountID, AmountCents: 100,
	})
	if !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatalf("expected invalid parent relationship, got %v", err)
	}
}

func TestAuthorizationUsesPendingBalanceAsTheHold(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	fundTestCard(t, ledgerService, 1_000)

	authorization, status, err := ledgerService.Authorize(context.Background(), domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 600,
	})
	if err != nil || status != 200 || authorization.Status != "approved" {
		t.Fatalf("authorization: status=%d response=%+v err=%v", status, authorization, err)
	}
	balance, err := ledgerService.Balance(testCardAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if balance.PostedBalance != "1000" || balance.AvailableBalance != "400" || balance.DebitsPending != "600" {
		t.Fatalf("hold not represented in pending balance: %+v", balance)
	}
	blockedReturn, status, err := ledgerService.ReturnFromCard(domain.CardReturnRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, CardID: testCardAccountID, AmountCents: 500,
	})
	if err != nil || status != 422 || blockedReturn.ErrorCode != "insufficient_funds" {
		t.Fatalf("return of held funds: status=%d response=%+v err=%v", status, blockedReturn, err)
	}

	declined, status, err := ledgerService.Authorize(context.Background(), domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 500,
	})
	if err != nil || status != 200 || declined.Status != "declined" || declined.ErrorCode != "insufficient_funds" {
		t.Fatalf("expected insufficient-funds decline: status=%d response=%+v err=%v", status, declined, err)
	}
	settlementBalance, err := ledgerService.Balance(ledger.CardSettlementPayableAccountID().String())
	if err != nil {
		t.Fatal(err)
	}
	if settlementBalance.CreditsPending != "600" {
		t.Fatalf("authorization not reflected in settlement payable: %+v", settlementBalance)
	}
}

func TestConcurrentAuthorizationsCannotDoubleSpend(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	fundTestCard(t, ledgerService, 50)

	responses := make(chan domain.AuthorizationResponse, 100)
	errors := make(chan error, 100)
	var requests sync.WaitGroup
	for range 100 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			response, status, err := ledgerService.Authorize(context.Background(), domain.AuthorizationRequest{
				RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 1,
			})
			if err != nil {
				errors <- err
				return
			}
			if status != http.StatusOK {
				errors <- fmt.Errorf("unexpected HTTP status %d", status)
				return
			}
			responses <- response
		}()
	}
	requests.Wait()
	close(responses)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}

	approved, declined := 0, 0
	for response := range responses {
		switch response.Status {
		case "approved":
			approved++
		case "declined":
			declined++
		default:
			t.Fatalf("unexpected authorization response: %+v", response)
		}
	}
	if approved != 50 || declined != 50 {
		t.Fatalf("approved=%d declined=%d, want 50 each", approved, declined)
	}
	card := assertBalance(t, ledgerService, testCardAccountID, "50", "0")
	if card.DebitsPending != "50" {
		t.Fatalf("unexpected pending balance: %+v", card)
	}
}

func TestAuthorizationIncrementUsesSamePartiesAndGroup(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	fundTestCard(t, ledgerService, 1_000)
	authorization, _, err := ledgerService.Authorize(context.Background(), domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	increment, status, err := ledgerService.IncrementAuthorization(context.Background(), domain.IncrementAuthorizationRequest{
		RequestID: testID(), AuthorizationID: authorization.AuthorizationID, IncrementCents: 100,
	})
	if err != nil || status != 200 || increment.Status != "approved" {
		t.Fatalf("increment: status=%d response=%+v err=%v", status, increment, err)
	}
	incrementID, _ := tb.HexStringToUint128(increment.HoldTransferID)
	transfer := ledgerClient.transfers[incrementID.String()]
	authorizationID, _ := tb.HexStringToUint128(authorization.AuthorizationID)
	if transfer.UserData128 != authorizationID {
		t.Fatal("increment is not grouped under the original authorization")
	}
}

func TestAuthorizationIncrementRejectsTransferOutsideAuthorizationRecipe(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	fundTestCard(t, ledgerService, 1_000)
	authorization, _, err := ledgerService.Authorize(context.Background(), domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationID, err := tb.HexStringToUint128(authorization.AuthorizationID)
	if err != nil {
		t.Fatal(err)
	}
	ledgerClient.mu.Lock()
	original := ledgerClient.transfers[authorizationID.String()]
	original.CreditAccountID, _ = tb.HexStringToUint128(testCorporateAccountID)
	ledgerClient.transfers[authorizationID.String()] = original
	ledgerClient.mu.Unlock()

	response, status, err := ledgerService.IncrementAuthorization(context.Background(), domain.IncrementAuthorizationRequest{
		RequestID: testID(), AuthorizationID: authorization.AuthorizationID, IncrementCents: 10,
	})
	if err != nil || status != http.StatusConflict || response.ErrorCode != "authorization_not_open" {
		t.Fatalf("status=%d response=%+v err=%v", status, response, err)
	}
}

func TestAuthorizationRequestIDDetectsChangedMerchant(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	fundTestCard(t, ledgerService, 1_000)
	request := domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 100,
	}
	if _, _, err := ledgerService.Authorize(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.MerchantID = "MRC_CHANGED"
	if _, _, err := ledgerService.Authorize(context.Background(), request); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected merchant idempotency conflict, got %v", err)
	}
}

func TestAuthorizationReplayRemainsApprovedAfterRiskPolicyChanges(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	approvingService := newTestServiceWithRisk(t, ledgerClient, 0, 100)
	fundTestCard(t, approvingService, 1_000)
	request := domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 50,
	}
	if _, _, err := approvingService.Authorize(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	decliningService := newTestServiceWithRisk(t, ledgerClient, 0, 1)
	replay, status, err := decliningService.Authorize(context.Background(), request)
	if err != nil || status != http.StatusOK || replay.Status != "approved" || replay.LedgerStatus != tb.TransferExists.String() {
		t.Fatalf("replay: status=%d response=%+v err=%v", status, replay, err)
	}
}

func TestAuthorizationIncrementRunsRiskEvaluationAgain(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestServiceWithRisk(t, ledgerClient, 0, 100)
	fundTestCard(t, ledgerService, 1_000)
	authorization, _, err := ledgerService.Authorize(context.Background(), domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 50,
	})
	if err != nil {
		t.Fatal(err)
	}

	incrementRequestID := testID()
	response, status, err := ledgerService.IncrementAuthorization(context.Background(), domain.IncrementAuthorizationRequest{
		RequestID: incrementRequestID, AuthorizationID: authorization.AuthorizationID, IncrementCents: 101,
	})
	if err != nil || status != 200 || response.Status != "declined" || response.ErrorCode != "risk_limit_exceeded" {
		t.Fatalf("risk decline: status=%d response=%+v err=%v", status, response, err)
	}
	incrementID, _ := tb.HexStringToUint128(incrementRequestID)
	if _, found := ledgerClient.transfers[incrementID.String()]; found {
		t.Fatal("risk-declined increment was submitted to TigerBeetle")
	}
}

func TestAuthorizationIncrementReplayRemainsApprovedAfterRiskPolicyChanges(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	approvingService := newTestServiceWithRisk(t, ledgerClient, 0, 100)
	fundTestCard(t, approvingService, 1_000)
	authorization, _, err := approvingService.Authorize(context.Background(), domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := domain.IncrementAuthorizationRequest{
		RequestID: testID(), AuthorizationID: authorization.AuthorizationID, IncrementCents: 50,
	}
	if _, _, err := approvingService.IncrementAuthorization(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	decliningService := newTestServiceWithRisk(t, ledgerClient, 0, 1)
	replay, status, err := decliningService.IncrementAuthorization(context.Background(), request)
	if err != nil || status != http.StatusOK || replay.Status != "approved" || replay.LedgerStatus != tb.TransferExists.String() {
		t.Fatalf("replay: status=%d response=%+v err=%v", status, replay, err)
	}
}

func TestAuthorizationIncrementReplayIgnoresDerivedTimeoutDrift(t *testing.T) {
	t.Parallel()
	ledgerClient := newFakeLedger()
	ledgerService := newTestService(t, ledgerClient)
	fundTestCard(t, ledgerService, 1_000)
	authorization, _, err := ledgerService.Authorize(context.Background(), domain.AuthorizationRequest{
		RequestID: testID(), CardID: testCardAccountID, MerchantID: testMerchantID, AmountCents: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := domain.IncrementAuthorizationRequest{
		RequestID: testID(), AuthorizationID: authorization.AuthorizationID, IncrementCents: 100,
	}
	if _, _, err := ledgerService.IncrementAuthorization(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	incrementID, err := tb.HexStringToUint128(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	ledgerClient.mu.Lock()
	existing := ledgerClient.transfers[incrementID.String()]
	existing.Timeout++
	ledgerClient.transfers[incrementID.String()] = existing
	ledgerClient.mu.Unlock()

	replay, status, err := ledgerService.IncrementAuthorization(context.Background(), request)
	if err != nil || status != http.StatusOK || replay.Status != "approved" || replay.LedgerStatus != tb.TransferExists.String() {
		t.Fatalf("replay: status=%d response=%+v err=%v", status, replay, err)
	}
}

func newTestService(t *testing.T, ledgerClient *fakeLedger) *Service {
	return newTestServiceWithRisk(t, ledgerClient, 0, 100_000)
}

func newTestServiceWithRisk(t *testing.T, ledgerClient *fakeLedger, riskDelay time.Duration, riskLimit uint64) *Service {
	t.Helper()
	ledgerService := New(
		ledgerClient,
		1,
		time.Hour,
		riskDelay,
		riskLimit,
		slog.New(slog.DiscardHandler),
		nil,
	)
	if err := ledgerService.EnsureSystemAccounts(); err != nil {
		t.Fatal(err)
	}
	return ledgerService
}

func testID() string {
	return tb.ID().String()
}

func createTestAccount(t *testing.T, ledgerService *Service, kind domain.AccountKind, id, corporateAccountID string) {
	t.Helper()
	if _, _, err := ledgerService.CreateAccount(domain.CreateAccountRequest{
		AccountID: id, Kind: kind, CorporateAccountID: corporateAccountID,
	}); err != nil {
		t.Fatal(err)
	}
}

func createCorporateAndCard(t *testing.T, ledgerService *Service) {
	t.Helper()
	createTestAccount(t, ledgerService, domain.AccountCorporateWallet, testCorporateAccountID, "")
	createTestAccount(t, ledgerService, domain.AccountCardWallet, testCardAccountID, testCorporateAccountID)
}

func fundTestCard(t *testing.T, ledgerService *Service, amount uint64) {
	t.Helper()
	createCorporateAndCard(t, ledgerService)
	if _, _, err := ledgerService.TopUp(domain.TopUpRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, AmountCents: amount,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ledgerService.AllocateToCard(domain.CardAllocationRequest{
		RequestID: testID(), AccountID: testCorporateAccountID, CardID: testCardAccountID, AmountCents: amount,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertBalance(
	t *testing.T,
	ledgerService *Service,
	accountID, posted, available string,
) domain.BalanceResponse {
	t.Helper()
	balance, err := ledgerService.Balance(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if balance.PostedBalance != posted || balance.AvailableBalance != available {
		t.Fatalf("unexpected balance: got %+v, want posted=%s available=%s", balance, posted, available)
	}
	return balance
}

type fakeLedger struct {
	mu              sync.Mutex
	accounts        map[string]tb.Account
	transfers       map[string]tb.Transfer
	failed          map[string]bool
	timestamp       uint64
	failAfterCommit bool
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{
		accounts:  make(map[string]tb.Account),
		transfers: make(map[string]tb.Transfer),
		failed:    make(map[string]bool),
		timestamp: uint64(time.Now().UnixNano()),
	}
}

func (f *fakeLedger) CreateAccount(account tb.Account) (tb.CreateAccountResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, found := f.accounts[account.ID.String()]; found {
		if existing.Flags != account.Flags {
			return tb.CreateAccountResult{Status: tb.AccountExistsWithDifferentFlags}, nil
		}
		if existing.UserData128 != account.UserData128 {
			return tb.CreateAccountResult{Status: tb.AccountExistsWithDifferentUserData128}, nil
		}
		if existing.UserData64 != account.UserData64 {
			return tb.CreateAccountResult{Status: tb.AccountExistsWithDifferentUserData64}, nil
		}
		if existing.UserData32 != account.UserData32 {
			return tb.CreateAccountResult{Status: tb.AccountExistsWithDifferentUserData32}, nil
		}
		if existing.Ledger != account.Ledger {
			return tb.CreateAccountResult{Status: tb.AccountExistsWithDifferentLedger}, nil
		}
		if existing.Code != account.Code {
			return tb.CreateAccountResult{Status: tb.AccountExistsWithDifferentCode}, nil
		}
		return tb.CreateAccountResult{Status: tb.AccountExists, Timestamp: existing.Timestamp}, nil
	}
	f.timestamp++
	account.Timestamp = f.timestamp
	f.accounts[account.ID.String()] = account
	return tb.CreateAccountResult{Status: tb.AccountCreated, Timestamp: account.Timestamp}, nil
}

func (f *fakeLedger) CreateTransfer(transfer tb.Transfer) (tb.CreateTransferResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed[transfer.ID.String()] {
		return tb.CreateTransferResult{Status: tb.TransferIDAlreadyFailed}, nil
	}
	if existing, found := f.transfers[transfer.ID.String()]; found {
		if existing.Flags != transfer.Flags {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentFlags}, nil
		}
		if existing.Timeout != transfer.Timeout {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentTimeout}, nil
		}
		if existing.DebitAccountID != transfer.DebitAccountID {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentDebitAccountID}, nil
		}
		if existing.CreditAccountID != transfer.CreditAccountID {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentCreditAccountID}, nil
		}
		if existing.Amount != transfer.Amount {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentAmount}, nil
		}
		if existing.UserData128 != transfer.UserData128 {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentUserData128}, nil
		}
		if existing.UserData64 != transfer.UserData64 {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentUserData64}, nil
		}
		if existing.UserData32 != transfer.UserData32 {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentUserData32}, nil
		}
		if existing.PendingID != transfer.PendingID {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentPendingID}, nil
		}
		if existing.Ledger != transfer.Ledger {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentLedger}, nil
		}
		if existing.Code != transfer.Code {
			return tb.CreateTransferResult{Status: tb.TransferExistsWithDifferentCode}, nil
		}
		return tb.CreateTransferResult{Status: tb.TransferExists, Timestamp: existing.Timestamp}, nil
	}
	debit, debitFound := f.accounts[transfer.DebitAccountID.String()]
	if !debitFound {
		f.failed[transfer.ID.String()] = true
		return tb.CreateTransferResult{Status: tb.TransferDebitAccountNotFound}, nil
	}
	credit, creditFound := f.accounts[transfer.CreditAccountID.String()]
	if !creditFound {
		f.failed[transfer.ID.String()] = true
		return tb.CreateTransferResult{Status: tb.TransferCreditAccountNotFound}, nil
	}
	amount, high := transfer.Amount.Uint64()
	if high != 0 {
		return tb.CreateTransferResult{Status: tb.TransferOverflowsDebits}, nil
	}
	debitsPending, _ := debit.DebitsPending.Uint64()
	debitsPosted, _ := debit.DebitsPosted.Uint64()
	creditsPosted, _ := debit.CreditsPosted.Uint64()
	if debit.AccountFlags().DebitsMustNotExceedCredits && debitsPending+debitsPosted+amount > creditsPosted {
		f.failed[transfer.ID.String()] = true
		return tb.CreateTransferResult{Status: tb.TransferExceedsCredits}, nil
	}
	creditsPending, _ := credit.CreditsPending.Uint64()
	creditDebitsPosted, _ := credit.DebitsPosted.Uint64()
	creditCreditsPosted, _ := credit.CreditsPosted.Uint64()
	if credit.AccountFlags().CreditsMustNotExceedDebits && creditsPending+creditCreditsPosted+amount > creditDebitsPosted {
		f.failed[transfer.ID.String()] = true
		return tb.CreateTransferResult{Status: tb.TransferExceedsDebits}, nil
	}

	f.timestamp++
	transfer.Timestamp = f.timestamp
	if transfer.TransferFlags().Pending {
		debit.DebitsPending = tb.ToUint128(debitsPending + amount)
		credit.CreditsPending = tb.ToUint128(creditsPending + amount)
	} else {
		debit.DebitsPosted = tb.ToUint128(debitsPosted + amount)
		credit.CreditsPosted = tb.ToUint128(creditCreditsPosted + amount)
	}
	f.accounts[debit.ID.String()] = debit
	f.accounts[credit.ID.String()] = credit
	f.transfers[transfer.ID.String()] = transfer
	result := tb.CreateTransferResult{Status: tb.TransferCreated, Timestamp: transfer.Timestamp}
	if f.failAfterCommit {
		f.failAfterCommit = false
		return tb.CreateTransferResult{}, fmt.Errorf("simulated response loss")
	}
	return result, nil
}

func (f *fakeLedger) LookupAccount(id tb.Uint128) (tb.Account, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	account, found := f.accounts[id.String()]
	return account, found, nil
}

func (f *fakeLedger) LookupTransfer(id tb.Uint128) (tb.Transfer, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	transfer, found := f.transfers[id.String()]
	return transfer, found, nil
}
