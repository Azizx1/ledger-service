package domain

import "time"

type AccountKind string

const (
	AccountSafeguardedCash       AccountKind = "safeguarded_cash"
	AccountCorporateWallet       AccountKind = "corporate_wallet"
	AccountCardWallet            AccountKind = "card_wallet"
	AccountCardSettlementPayable AccountKind = "card_settlement_payable"
)

func (k AccountKind) Valid() bool {
	switch k {
	case AccountSafeguardedCash,
		AccountCorporateWallet,
		AccountCardWallet,
		AccountCardSettlementPayable:
		return true
	default:
		return false
	}
}

type OperationKind string

const (
	OperationTopUp                  OperationKind = "topup"
	OperationWithdrawal             OperationKind = "withdrawal"
	OperationCardAllocation         OperationKind = "card_allocation"
	OperationCardReturn             OperationKind = "card_return"
	OperationAuthorization          OperationKind = "authorization"
	OperationAuthorizationIncrement OperationKind = "authorization_increment"
)

type CreateAccountRequest struct {
	AccountID          string      `json:"account_id"`
	Kind               AccountKind `json:"kind"`
	CorporateAccountID string      `json:"corporate_account_id,omitempty"`
}

type AccountResponse struct {
	AccountID          string      `json:"account_id"`
	Kind               AccountKind `json:"kind"`
	CorporateAccountID string      `json:"corporate_account_id,omitempty"`
	Status             string      `json:"status"`
	Timestamp          uint64      `json:"timestamp"`
}

type BalanceResponse struct {
	AccountID          string      `json:"account_id"`
	Kind               AccountKind `json:"kind"`
	CorporateAccountID string      `json:"corporate_account_id,omitempty"`
	DebitsPending      string      `json:"debits_pending"`
	DebitsPosted       string      `json:"debits_posted"`
	CreditsPending     string      `json:"credits_pending"`
	CreditsPosted      string      `json:"credits_posted"`
	PostedBalance      string      `json:"posted_balance"`
	AvailableBalance   string      `json:"available_balance"`
	Timestamp          uint64      `json:"timestamp"`
}

type TopUpRequest struct {
	RequestID   string `json:"request_id"`
	AccountID   string `json:"account_id"`
	AmountCents uint64 `json:"amount_cents"`
}

type WithdrawalRequest struct {
	RequestID   string `json:"request_id"`
	AccountID   string `json:"account_id"`
	AmountCents uint64 `json:"amount_cents"`
}

type CardAllocationRequest struct {
	RequestID   string `json:"request_id"`
	AccountID   string `json:"account_id"`
	CardID      string `json:"card_id"`
	AmountCents uint64 `json:"amount_cents"`
}

type CardReturnRequest struct {
	RequestID   string `json:"request_id"`
	AccountID   string `json:"account_id"`
	CardID      string `json:"card_id"`
	AmountCents uint64 `json:"amount_cents"`
}

type AuthorizationRequest struct {
	RequestID   string `json:"request_id"`
	CardID      string `json:"card_id"`
	MerchantID  string `json:"merchant_id"`
	AmountCents uint64 `json:"amount_cents"`
}

type IncrementAuthorizationRequest struct {
	RequestID       string `json:"request_id"`
	AuthorizationID string `json:"authorization_id"`
	IncrementCents  uint64 `json:"increment_cents"`
}

type TransactionResponse struct {
	RequestID     string `json:"request_id"`
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
	LedgerStatus  string `json:"ledger_status"`
	Timestamp     uint64 `json:"timestamp,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	Message       string `json:"message,omitempty"`
}

type AuthorizationResponse struct {
	RequestID        string     `json:"request_id"`
	AuthorizationID  string     `json:"authorization_id"`
	HoldTransferID   string     `json:"hold_transfer_id,omitempty"`
	Status           string     `json:"status"`
	LedgerStatus     string     `json:"ledger_status"`
	EvaluationTimeMS int64      `json:"evaluation_time_ms"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	Timestamp        uint64     `json:"timestamp,omitempty"`
	ErrorCode        string     `json:"error_code,omitempty"`
	Message          string     `json:"message,omitempty"`
}

type IncrementAuthorizationResponse struct {
	RequestID        string `json:"request_id"`
	AuthorizationID  string `json:"authorization_id"`
	HoldTransferID   string `json:"hold_transfer_id,omitempty"`
	Status           string `json:"status"`
	LedgerStatus     string `json:"ledger_status"`
	IncrementCents   uint64 `json:"increment_cents"`
	EvaluationTimeMS int64  `json:"evaluation_time_ms"`
	Timestamp        uint64 `json:"timestamp,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	Message          string `json:"message,omitempty"`
}

type ErrorResponse struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}
