package service

import (
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abdulaziz/ledger-service/internal/domain"
	"github.com/abdulaziz/ledger-service/internal/ledger"
	tb "github.com/tigerbeetle/tigerbeetle-go"
)

// Ledger is the small TigerBeetle surface the service needs. The production
// adapter and the deterministic test ledger both implement it.
type Ledger interface {
	CreateAccount(tb.Account) (tb.CreateAccountResult, error)
	CreateTransfer(tb.Transfer) (tb.CreateTransferResult, error)
	LookupAccount(tb.Uint128) (tb.Account, bool, error)
	LookupTransfer(tb.Uint128) (tb.Transfer, bool, error)
}

type Metrics interface {
	ObserveOperation(kind, outcome string, elapsed time.Duration)
}

type accountMetadata struct {
	kind               domain.AccountKind
	corporateAccountID tb.Uint128
}

type Service struct {
	ledger               Ledger
	ledgerID             uint32
	authorizationTimeout time.Duration
	riskDelay            time.Duration
	riskLimitCents       uint64
	logger               *slog.Logger
	metrics              Metrics
	accountMetadata      sync.Map
}

func New(
	ledgerClient Ledger,
	ledgerID uint32,
	authorizationTimeout time.Duration,
	riskDelay time.Duration,
	riskLimitCents uint64,
	logger *slog.Logger,
	metrics Metrics,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	authorizationTimeout = ((authorizationTimeout + time.Second - 1) / time.Second) * time.Second
	return &Service{
		ledger:               ledgerClient,
		ledgerID:             ledgerID,
		authorizationTimeout: authorizationTimeout,
		riskDelay:            riskDelay,
		riskLimitCents:       riskLimitCents,
		logger:               logger,
		metrics:              metrics,
	}
}

func (s *Service) EnsureSystemAccounts() error {
	systemAccounts := []struct {
		kind domain.AccountKind
		id   tb.Uint128
	}{
		{kind: domain.AccountSafeguardedCash, id: ledger.SafeguardedCashAccountID()},
		{kind: domain.AccountCardSettlementPayable, id: ledger.CardSettlementPayableAccountID()},
	}

	for _, systemAccount := range systemAccounts {
		account, err := ledger.Account(systemAccount.kind, systemAccount.id, tb.Uint128{}, s.ledgerID)
		if err != nil {
			return err
		}
		result, err := s.ledger.CreateAccount(account)
		if err != nil {
			return err
		}
		if result.Status != tb.AccountCreated && result.Status != tb.AccountExists {
			return fmt.Errorf("provision %s account: %s", systemAccount.kind, result.Status)
		}
		s.rememberAccount(account)
	}
	return nil
}

func (s *Service) CreateAccount(request domain.CreateAccountRequest) (domain.AccountResponse, int, error) {
	id, err := accountID("account_id", request.AccountID)
	if err != nil {
		return domain.AccountResponse{}, 0, err
	}
	if request.Kind != domain.AccountCorporateWallet && request.Kind != domain.AccountCardWallet {
		return domain.AccountResponse{}, 0, fmt.Errorf("%w: kind must be corporate_wallet or card_wallet", domain.ErrInvalidRequest)
	}

	var corporateAccountID tb.Uint128
	if request.Kind == domain.AccountCardWallet {
		corporateAccountID, err = accountID("corporate_account_id", request.CorporateAccountID)
		if err != nil {
			return domain.AccountResponse{}, 0, err
		}
		if corporateAccountID == id {
			return domain.AccountResponse{}, 0, fmt.Errorf("%w: card account cannot be its own corporate account", domain.ErrInvalidRequest)
		}
		if _, err := s.requireAccountKind(corporateAccountID, domain.AccountCorporateWallet); err != nil {
			return domain.AccountResponse{}, 0, err
		}
	} else if request.CorporateAccountID != "" {
		return domain.AccountResponse{}, 0, fmt.Errorf("%w: corporate_account_id is only valid for card_wallet", domain.ErrInvalidRequest)
	}

	account, err := ledger.Account(request.Kind, id, corporateAccountID, s.ledgerID)
	if err != nil {
		return domain.AccountResponse{}, 0, err
	}
	result, err := s.ledger.CreateAccount(account)
	if err != nil {
		return domain.AccountResponse{}, 0, err
	}

	response := domain.AccountResponse{
		AccountID:          account.ID.String(),
		Kind:               request.Kind,
		CorporateAccountID: publicID(corporateAccountID),
		Timestamp:          result.Timestamp,
	}
	switch result.Status {
	case tb.AccountCreated:
		s.rememberAccount(account)
		response.Status = "created"
		return response, http.StatusCreated, nil
	case tb.AccountExists:
		s.rememberAccount(account)
		response.Status = "exists"
		return response, http.StatusOK, nil
	default:
		if isAccountIdempotencyConflict(result.Status) {
			return domain.AccountResponse{}, 0, domain.ErrIdempotencyConflict
		}
		return domain.AccountResponse{}, 0, fmt.Errorf("create account rejected: %s", result.Status)
	}
}

func (s *Service) Balance(accountIDValue string) (domain.BalanceResponse, error) {
	if err := validateExternalID("account_id", accountIDValue); err != nil {
		return domain.BalanceResponse{}, err
	}
	id, err := parsePublicTigerBeetleID(accountIDValue)
	if err != nil {
		return domain.BalanceResponse{}, fmt.Errorf("%w: account_id must be a non-zero 128-bit hexadecimal TigerBeetle ID", domain.ErrInvalidRequest)
	}
	account, found, err := s.ledger.LookupAccount(id)
	if err != nil {
		return domain.BalanceResponse{}, err
	}
	if !found {
		return domain.BalanceResponse{}, domain.ErrAccountNotFound
	}
	metadata, ok := s.metadataForAccount(account)
	if !ok {
		return domain.BalanceResponse{}, domain.ErrAccountNotFound
	}
	s.rememberAccount(account)

	posted, available := balances(account, metadata.kind)
	return domain.BalanceResponse{
		AccountID:          account.ID.String(),
		Kind:               metadata.kind,
		CorporateAccountID: publicID(account.UserData128),
		DebitsPending:      account.DebitsPending.BigInt().String(),
		DebitsPosted:       account.DebitsPosted.BigInt().String(),
		CreditsPending:     account.CreditsPending.BigInt().String(),
		CreditsPosted:      account.CreditsPosted.BigInt().String(),
		PostedBalance:      posted.String(),
		AvailableBalance:   available.String(),
		Timestamp:          account.Timestamp,
	}, nil
}

func balances(account tb.Account, kind domain.AccountKind) (*big.Int, *big.Int) {
	debitsPosted := account.DebitsPosted.BigInt()
	creditsPosted := account.CreditsPosted.BigInt()
	debitsPending := account.DebitsPending.BigInt()
	creditsPending := account.CreditsPending.BigInt()

	if kind == domain.AccountSafeguardedCash {
		posted := new(big.Int).Sub(debitsPosted, creditsPosted)
		available := new(big.Int).Sub(new(big.Int).Set(posted), creditsPending)
		return posted, available
	}

	posted := new(big.Int).Sub(creditsPosted, debitsPosted)
	available := new(big.Int).Sub(new(big.Int).Set(posted), debitsPending)
	return posted, available
}

func requestID(value string) (tb.Uint128, error) {
	if err := validateExternalID("request_id", value); err != nil {
		return tb.Uint128{}, err
	}
	id, err := parsePublicTigerBeetleID(value)
	if err != nil {
		return tb.Uint128{}, fmt.Errorf("%w: request_id must be a non-zero 128-bit hexadecimal TigerBeetle ID", domain.ErrInvalidRequest)
	}
	return id, nil
}

func accountID(field, value string) (tb.Uint128, error) {
	if err := validateExternalID(field, value); err != nil {
		return tb.Uint128{}, err
	}
	id, err := parsePublicTigerBeetleID(value)
	if err != nil || ledger.IsSystemAccountID(id) {
		return tb.Uint128{}, fmt.Errorf("%w: %s must be a non-zero 128-bit hexadecimal TigerBeetle ID outside the reserved system range", domain.ErrInvalidRequest, field)
	}
	return id, nil
}

func publicID(id tb.Uint128) string {
	if id == (tb.Uint128{}) {
		return ""
	}
	return id.String()
}

func (s *Service) rememberAccount(account tb.Account) {
	metadata, ok := s.metadataForAccount(account)
	if !ok {
		return
	}
	s.accountMetadata.Store(account.ID, metadata)
}

func (s *Service) requireAccountKind(id tb.Uint128, expected domain.AccountKind) (accountMetadata, error) {
	if cached, ok := s.accountMetadata.Load(id); ok {
		metadata := cached.(accountMetadata)
		if metadata.kind == expected {
			return metadata, nil
		}
		return accountMetadata{}, domain.ErrAccountNotFound
	}

	account, found, err := s.ledger.LookupAccount(id)
	if err != nil {
		return accountMetadata{}, err
	}
	metadata, ok := s.metadataForAccount(account)
	if !found || !ok || metadata.kind != expected {
		return accountMetadata{}, domain.ErrAccountNotFound
	}
	s.accountMetadata.Store(id, metadata)
	return metadata, nil
}

func (s *Service) metadataForAccount(account tb.Account) (accountMetadata, bool) {
	kind, ok := accountKind(account)
	if !ok || account.Ledger != s.ledgerID {
		return accountMetadata{}, false
	}
	if (kind == domain.AccountSafeguardedCash && account.ID != ledger.SafeguardedCashAccountID()) ||
		(kind == domain.AccountCardSettlementPayable && account.ID != ledger.CardSettlementPayableAccountID()) ||
		((kind == domain.AccountCorporateWallet || kind == domain.AccountCardWallet) && ledger.IsSystemAccountID(account.ID)) {
		return accountMetadata{}, false
	}
	expected, err := ledger.Account(kind, account.ID, account.UserData128, s.ledgerID)
	if err != nil || account.Flags != expected.Flags || account.UserData64 != 0 || account.UserData32 != 0 {
		return accountMetadata{}, false
	}
	return accountMetadata{kind: kind, corporateAccountID: account.UserData128}, true
}

func parsePublicTigerBeetleID(value string) (tb.Uint128, error) {
	id, err := tb.HexStringToUint128(value)
	if err != nil || id == (tb.Uint128{}) || isUint128Max(id) {
		return tb.Uint128{}, fmt.Errorf("invalid TigerBeetle ID")
	}
	return id, nil
}

func isUint128Max(id tb.Uint128) bool {
	for _, value := range id.Bytes() {
		if value != 0xff {
			return false
		}
	}
	return true
}

func validateExternalID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", domain.ErrInvalidRequest, field)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%w: %s must not have surrounding whitespace", domain.ErrInvalidRequest, field)
	}
	if len(value) > 200 {
		return fmt.Errorf("%w: %s must not exceed 200 bytes", domain.ErrInvalidRequest, field)
	}
	return nil
}

func isIdempotencyConflict(status tb.CreateTransferStatus) bool {
	switch status {
	case tb.TransferExistsWithDifferentFlags,
		tb.TransferExistsWithDifferentPendingID,
		tb.TransferExistsWithDifferentTimeout,
		tb.TransferExistsWithDifferentDebitAccountID,
		tb.TransferExistsWithDifferentCreditAccountID,
		tb.TransferExistsWithDifferentAmount,
		tb.TransferExistsWithDifferentUserData128,
		tb.TransferExistsWithDifferentUserData64,
		tb.TransferExistsWithDifferentUserData32,
		tb.TransferExistsWithDifferentLedger,
		tb.TransferExistsWithDifferentCode:
		return true
	default:
		return false
	}
}

func isAccountIdempotencyConflict(status tb.CreateAccountStatus) bool {
	switch status {
	case tb.AccountExistsWithDifferentFlags,
		tb.AccountExistsWithDifferentUserData128,
		tb.AccountExistsWithDifferentUserData64,
		tb.AccountExistsWithDifferentUserData32,
		tb.AccountExistsWithDifferentLedger,
		tb.AccountExistsWithDifferentCode:
		return true
	default:
		return false
	}
}

func accountKind(account tb.Account) (domain.AccountKind, bool) {
	switch account.Code {
	case ledger.AccountCodeSafeguardedCash:
		return domain.AccountSafeguardedCash, true
	case ledger.AccountCodeCorporateWallet:
		return domain.AccountCorporateWallet, true
	case ledger.AccountCodeCardWallet:
		return domain.AccountCardWallet, true
	case ledger.AccountCodeCardSettlementPayable:
		return domain.AccountCardSettlementPayable, true
	default:
		return "", false
	}
}

func errorCode(status tb.CreateTransferStatus) (string, string, int) {
	switch status {
	case tb.TransferExceedsCredits, tb.TransferExceedsDebits:
		return "insufficient_funds", "the debit account has insufficient available funds", http.StatusUnprocessableEntity
	case tb.TransferDebitAccountNotFound, tb.TransferCreditAccountNotFound:
		return "account_not_found", "one or more ledger accounts do not exist", http.StatusNotFound
	case tb.TransferIDAlreadyFailed:
		return "previous_attempt_failed", "this transfer ID was previously rejected and cannot produce a different outcome", http.StatusConflict
	default:
		return "ledger_invariant_violation", "the transfer violated an internal ledger recipe", http.StatusInternalServerError
	}
}
