package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/abdulaziz/ledger-service/internal/domain"
	"github.com/abdulaziz/ledger-service/internal/ledger"
	tb "github.com/tigerbeetle/tigerbeetle-go"
)

func (s *Service) TopUp(request domain.TopUpRequest) (domain.TransactionResponse, int, error) {
	account, err := accountID("account_id", request.AccountID)
	if err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	if request.AmountCents == 0 {
		return domain.TransactionResponse{}, 0, fmt.Errorf("%w: amount_cents must be positive", domain.ErrInvalidRequest)
	}
	id, err := requestID(request.RequestID)
	if err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	if _, err := s.requireAccountKind(account, domain.AccountCorporateWallet); err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	transfer := tb.Transfer{
		ID:              id,
		DebitAccountID:  ledger.SafeguardedCashAccountID(),
		CreditAccountID: account,
		Amount:          tb.ToUint128(request.AmountCents),
		Ledger:          s.ledgerID,
		Code:            ledger.TransferCodeTopUp,
	}
	return s.executeTransaction(domain.OperationTopUp, request.RequestID, transfer, "posted")
}

func (s *Service) Withdraw(request domain.WithdrawalRequest) (domain.TransactionResponse, int, error) {
	account, err := accountID("account_id", request.AccountID)
	if err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	if request.AmountCents == 0 {
		return domain.TransactionResponse{}, 0, fmt.Errorf("%w: amount_cents must be positive", domain.ErrInvalidRequest)
	}
	id, err := requestID(request.RequestID)
	if err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	if _, err := s.requireAccountKind(account, domain.AccountCorporateWallet); err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	transfer := tb.Transfer{
		ID:              id,
		DebitAccountID:  account,
		CreditAccountID: ledger.SafeguardedCashAccountID(),
		Amount:          tb.ToUint128(request.AmountCents),
		Ledger:          s.ledgerID,
		Code:            ledger.TransferCodeWithdrawal,
	}
	return s.executeTransaction(domain.OperationWithdrawal, request.RequestID, transfer, "posted")
}

func (s *Service) AllocateToCard(request domain.CardAllocationRequest) (domain.TransactionResponse, int, error) {
	return s.moveCardFunds(
		domain.OperationCardAllocation,
		request.RequestID,
		request.AccountID,
		request.CardID,
		request.AmountCents,
		false,
	)
}

func (s *Service) ReturnFromCard(request domain.CardReturnRequest) (domain.TransactionResponse, int, error) {
	return s.moveCardFunds(
		domain.OperationCardReturn,
		request.RequestID,
		request.AccountID,
		request.CardID,
		request.AmountCents,
		true,
	)
}

func (s *Service) moveCardFunds(
	kind domain.OperationKind,
	requestIDValue, accountIDValue, cardIDValue string,
	amountCents uint64,
	returnToCorporate bool,
) (domain.TransactionResponse, int, error) {
	corporateAccountID, err := accountID("account_id", accountIDValue)
	if err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	cardAccountID, err := accountID("card_id", cardIDValue)
	if err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	if amountCents == 0 {
		return domain.TransactionResponse{}, 0, fmt.Errorf("%w: amount_cents must be positive", domain.ErrInvalidRequest)
	}
	id, err := requestID(requestIDValue)
	if err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	if _, err := s.requireAccountKind(corporateAccountID, domain.AccountCorporateWallet); err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	cardAccount, err := s.requireAccountKind(cardAccountID, domain.AccountCardWallet)
	if err != nil {
		return domain.TransactionResponse{}, 0, err
	}
	if cardAccount.corporateAccountID != corporateAccountID {
		return domain.TransactionResponse{}, 0, fmt.Errorf("%w: card_id is not linked to account_id", domain.ErrInvalidRequest)
	}

	debitAccountID := corporateAccountID
	creditAccountID := cardAccountID
	code := ledger.TransferCodeCardAllocation
	if returnToCorporate {
		debitAccountID, creditAccountID = cardAccountID, corporateAccountID
		code = ledger.TransferCodeCardReturn
	}
	transfer := tb.Transfer{
		ID:              id,
		DebitAccountID:  debitAccountID,
		CreditAccountID: creditAccountID,
		Amount:          tb.ToUint128(amountCents),
		Ledger:          s.ledgerID,
		Code:            code,
	}
	return s.executeTransaction(kind, requestIDValue, transfer, "posted")
}

func (s *Service) executeTransaction(
	kind domain.OperationKind,
	requestID string,
	transfer tb.Transfer,
	successStatus string,
) (domain.TransactionResponse, int, error) {
	started := time.Now()
	result, err := s.ledger.CreateTransfer(transfer)
	if err != nil {
		amount, _ := transfer.Amount.Uint64()
		s.observeCompletion(
			kind, requestID, "unknown", amount, started, "request_error",
			"error", err,
			"debit_account_id", transfer.DebitAccountID.String(),
			"credit_account_id", transfer.CreditAccountID.String(),
		)
		return domain.TransactionResponse{}, 0, err
	}
	if isIdempotencyConflict(result.Status) {
		return domain.TransactionResponse{}, 0, domain.ErrIdempotencyConflict
	}

	response := domain.TransactionResponse{
		RequestID:     requestID,
		TransactionID: requestID,
		LedgerStatus:  result.Status.String(),
		Timestamp:     result.Timestamp,
	}
	httpStatus := http.StatusOK
	if result.Status == tb.TransferCreated || result.Status == tb.TransferExists {
		response.Status = successStatus
	} else {
		response.Status = "failed"
		response.ErrorCode, response.Message, httpStatus = errorCode(result.Status)
	}

	amount, _ := transfer.Amount.Uint64()
	s.observeCompletion(
		kind,
		requestID,
		response.Status,
		amount,
		started,
		result.Status.String(),
		"debit_account_id", transfer.DebitAccountID.String(),
		"credit_account_id", transfer.CreditAccountID.String(),
	)
	return response, httpStatus, nil
}

func (s *Service) Authorize(ctx context.Context, request domain.AuthorizationRequest) (domain.AuthorizationResponse, int, error) {
	cardAccountID, err := accountID("card_id", request.CardID)
	if err != nil {
		return domain.AuthorizationResponse{}, 0, err
	}
	if err := validateExternalID("merchant_id", request.MerchantID); err != nil {
		return domain.AuthorizationResponse{}, 0, err
	}
	if request.AmountCents == 0 {
		return domain.AuthorizationResponse{}, 0, fmt.Errorf("%w: amount_cents must be positive", domain.ErrInvalidRequest)
	}
	id, err := requestID(request.RequestID)
	if err != nil {
		return domain.AuthorizationResponse{}, 0, err
	}

	started := time.Now()
	if _, err := s.requireAccountKind(cardAccountID, domain.AccountCardWallet); err != nil {
		return domain.AuthorizationResponse{}, 0, err
	}
	merchant64, merchant32 := merchantFingerprint(request.MerchantID)
	transfer := tb.Transfer{
		ID:              id,
		DebitAccountID:  cardAccountID,
		CreditAccountID: ledger.CardSettlementPayableAccountID(),
		Amount:          tb.ToUint128(request.AmountCents),
		UserData128:     id,
		UserData64:      merchant64,
		UserData32:      merchant32,
		Timeout:         uint32(s.authorizationTimeout / time.Second),
		Ledger:          s.ledgerID,
		Code:            ledger.TransferCodeAuthorization,
		Flags:           tb.TransferFlags{Pending: true}.ToUint16(),
	}
	riskStarted := time.Now()
	approved, err := s.evaluateRisk(ctx, request.AmountCents)
	if err != nil {
		return domain.AuthorizationResponse{}, 0, fmt.Errorf("evaluate risk: %w", err)
	}
	evaluationMS := time.Since(riskStarted).Milliseconds()
	if !approved {
		existing, found, lookupErr := s.ledger.LookupTransfer(id)
		if lookupErr != nil {
			return domain.AuthorizationResponse{}, 0, lookupErr
		}
		if found {
			if !sameTransfer(existing, transfer) {
				return domain.AuthorizationResponse{}, 0, domain.ErrIdempotencyConflict
			}
			expiresAt := time.Unix(0, int64(existing.Timestamp)).Add(s.authorizationTimeout).UTC()
			response := domain.AuthorizationResponse{
				RequestID:        request.RequestID,
				AuthorizationID:  request.RequestID,
				HoldTransferID:   request.RequestID,
				Status:           "approved",
				LedgerStatus:     tb.TransferExists.String(),
				EvaluationTimeMS: evaluationMS,
				ExpiresAt:        &expiresAt,
				Timestamp:        existing.Timestamp,
			}
			s.observeCompletion(
				domain.OperationAuthorization,
				request.RequestID,
				response.Status,
				request.AmountCents,
				started,
				response.LedgerStatus,
				"card_id", request.CardID,
				"merchant_id", request.MerchantID,
			)
			return response, http.StatusOK, nil
		}
		response := domain.AuthorizationResponse{
			RequestID:        request.RequestID,
			AuthorizationID:  request.RequestID,
			Status:           "declined",
			LedgerStatus:     "not_submitted",
			EvaluationTimeMS: evaluationMS,
			ErrorCode:        "risk_limit_exceeded",
			Message:          "authorization amount exceeds the automatic approval limit",
		}
		s.observeCompletion(
			domain.OperationAuthorization,
			request.RequestID,
			response.Status,
			request.AmountCents,
			started,
			response.LedgerStatus,
			"card_id", request.CardID,
			"merchant_id", request.MerchantID,
		)
		return response, http.StatusOK, nil
	}
	result, err := s.ledger.CreateTransfer(transfer)
	if err != nil {
		s.observeCompletion(
			domain.OperationAuthorization,
			request.RequestID,
			"unknown",
			request.AmountCents,
			started,
			"request_error",
			"error", err,
			"card_id", request.CardID,
			"merchant_id", request.MerchantID,
		)
		return domain.AuthorizationResponse{}, 0, err
	}
	if isIdempotencyConflict(result.Status) {
		return domain.AuthorizationResponse{}, 0, domain.ErrIdempotencyConflict
	}

	response := domain.AuthorizationResponse{
		RequestID:        request.RequestID,
		AuthorizationID:  request.RequestID,
		HoldTransferID:   request.RequestID,
		LedgerStatus:     result.Status.String(),
		EvaluationTimeMS: evaluationMS,
		Timestamp:        result.Timestamp,
	}
	httpStatus := http.StatusOK
	if result.Status == tb.TransferCreated || result.Status == tb.TransferExists {
		response.Status = "approved"
		expiresAt := time.Unix(0, int64(result.Timestamp)).Add(s.authorizationTimeout).UTC()
		response.ExpiresAt = &expiresAt
	} else {
		response.ErrorCode, response.Message, httpStatus = errorCode(result.Status)
		if response.ErrorCode == "insufficient_funds" {
			response.Status = "declined"
			httpStatus = http.StatusOK
		} else {
			response.Status = "failed"
		}
	}
	s.observeCompletion(
		domain.OperationAuthorization,
		request.RequestID,
		response.Status,
		request.AmountCents,
		started,
		result.Status.String(),
		"card_id", request.CardID,
		"merchant_id", request.MerchantID,
	)
	return response, httpStatus, nil
}

func merchantFingerprint(merchantID string) (uint64, uint32) {
	sum := sha256.Sum256([]byte("ledger-service:v1:merchant:" + merchantID))
	return binary.LittleEndian.Uint64(sum[:8]), binary.LittleEndian.Uint32(sum[8:12])
}

func (s *Service) evaluateRisk(ctx context.Context, amountCents uint64) (bool, error) {
	if s.riskDelay > 0 {
		timer := time.NewTimer(s.riskDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
		}
	}
	return amountCents <= s.riskLimitCents, nil
}

func (s *Service) IncrementAuthorization(ctx context.Context, request domain.IncrementAuthorizationRequest) (domain.IncrementAuthorizationResponse, int, error) {
	if err := validateExternalID("authorization_id", request.AuthorizationID); err != nil {
		return domain.IncrementAuthorizationResponse{}, 0, err
	}
	authorizationID, err := parsePublicTigerBeetleID(request.AuthorizationID)
	if err != nil {
		return domain.IncrementAuthorizationResponse{}, 0, fmt.Errorf("%w: authorization_id must be a TigerBeetle ID", domain.ErrInvalidRequest)
	}
	if request.IncrementCents == 0 {
		return domain.IncrementAuthorizationResponse{}, 0, fmt.Errorf("%w: increment_cents must be positive", domain.ErrInvalidRequest)
	}
	incrementID, err := requestID(request.RequestID)
	if err != nil {
		return domain.IncrementAuthorizationResponse{}, 0, err
	}
	if incrementID == authorizationID {
		return domain.IncrementAuthorizationResponse{}, 0, fmt.Errorf("%w: request_id must differ from authorization_id", domain.ErrInvalidRequest)
	}

	started := time.Now()
	original, found, err := s.ledger.LookupTransfer(authorizationID)
	if err != nil {
		return domain.IncrementAuthorizationResponse{}, 0, err
	}
	if !found {
		return domain.IncrementAuthorizationResponse{}, 0, domain.ErrAuthorizationNotFound
	}
	pendingFlag := tb.TransferFlags{Pending: true}.ToUint16()
	if original.Flags != pendingFlag ||
		original.Code != ledger.TransferCodeAuthorization ||
		original.Ledger != s.ledgerID ||
		original.CreditAccountID != ledger.CardSettlementPayableAccountID() ||
		original.UserData128 != original.ID ||
		original.PendingID != (tb.Uint128{}) ||
		original.Timeout == 0 {
		return incrementFailure(request, "authorization_not_open", "the transfer is not an open authorization", http.StatusConflict, 0)
	}
	if _, err := s.requireAccountKind(original.DebitAccountID, domain.AccountCardWallet); err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return incrementFailure(request, "authorization_not_open", "the transfer is not an open authorization", http.StatusConflict, 0)
		}
		return domain.IncrementAuthorizationResponse{}, 0, err
	}

	riskStarted := time.Now()
	approved, err := s.evaluateRisk(ctx, request.IncrementCents)
	if err != nil {
		return domain.IncrementAuthorizationResponse{}, 0, fmt.Errorf("evaluate risk: %w", err)
	}
	evaluationMS := time.Since(riskStarted).Milliseconds()
	if !approved {
		if response, status, found, lookupErr := s.lookupExistingIncrement(
			request, original, incrementID, authorizationID, started, evaluationMS,
		); lookupErr != nil || found {
			return response, status, lookupErr
		}
		response := domain.IncrementAuthorizationResponse{
			RequestID:        request.RequestID,
			AuthorizationID:  request.AuthorizationID,
			Status:           "declined",
			LedgerStatus:     "not_submitted",
			IncrementCents:   request.IncrementCents,
			EvaluationTimeMS: evaluationMS,
			ErrorCode:        "risk_limit_exceeded",
			Message:          "authorization increment exceeds the automatic approval limit",
		}
		s.observeCompletion(
			domain.OperationAuthorizationIncrement,
			request.RequestID,
			response.Status,
			request.IncrementCents,
			started,
			response.LedgerStatus,
			"authorization_id", request.AuthorizationID,
			"card_id", original.DebitAccountID.String(),
		)
		return response, http.StatusOK, nil
	}

	expiresAt := time.Unix(0, int64(original.Timestamp)).Add(time.Duration(original.Timeout) * time.Second)
	remaining := time.Until(expiresAt)
	if original.Timeout != 0 && remaining <= 0 {
		if response, status, found, lookupErr := s.lookupExistingIncrement(
			request, original, incrementID, authorizationID, started, evaluationMS,
		); lookupErr != nil || found {
			return response, status, lookupErr
		}
		return incrementFailure(request, "authorization_not_open", "the authorization has expired", http.StatusConflict, evaluationMS)
	}
	remainingSeconds := uint32((remaining + time.Second - 1) / time.Second)
	if remainingSeconds > original.Timeout {
		remainingSeconds = original.Timeout
	}

	transfer := tb.Transfer{
		ID:              incrementID,
		DebitAccountID:  original.DebitAccountID,
		CreditAccountID: original.CreditAccountID,
		Amount:          tb.ToUint128(request.IncrementCents),
		UserData128:     authorizationID,
		UserData64:      original.UserData64,
		UserData32:      original.UserData32,
		Timeout:         remainingSeconds,
		Ledger:          s.ledgerID,
		Code:            ledger.TransferCodeAuthorizationIncrement,
		Flags:           tb.TransferFlags{Pending: true}.ToUint16(),
	}
	result, err := s.ledger.CreateTransfer(transfer)
	if err != nil {
		s.observeCompletion(
			domain.OperationAuthorizationIncrement,
			request.RequestID,
			"unknown",
			request.IncrementCents,
			started,
			"request_error",
			"error", err,
			"authorization_id", request.AuthorizationID,
			"card_id", original.DebitAccountID.String(),
		)
		return domain.IncrementAuthorizationResponse{}, 0, err
	}
	if result.Status == tb.TransferExistsWithDifferentTimeout {
		existing, found, lookupErr := s.ledger.LookupTransfer(incrementID)
		if lookupErr != nil {
			return domain.IncrementAuthorizationResponse{}, 0, lookupErr
		}
		if found && sameTransferExceptTimeout(existing, transfer) {
			result = tb.CreateTransferResult{Status: tb.TransferExists, Timestamp: existing.Timestamp}
		}
	}
	if isIdempotencyConflict(result.Status) {
		return domain.IncrementAuthorizationResponse{}, 0, domain.ErrIdempotencyConflict
	}

	response := domain.IncrementAuthorizationResponse{
		RequestID:        request.RequestID,
		AuthorizationID:  request.AuthorizationID,
		HoldTransferID:   request.RequestID,
		LedgerStatus:     result.Status.String(),
		IncrementCents:   request.IncrementCents,
		EvaluationTimeMS: evaluationMS,
		Timestamp:        result.Timestamp,
	}
	httpStatus := http.StatusOK
	if result.Status == tb.TransferCreated || result.Status == tb.TransferExists {
		response.Status = "approved"
	} else {
		response.ErrorCode, response.Message, httpStatus = errorCode(result.Status)
		if response.ErrorCode == "insufficient_funds" {
			response.Status = "declined"
			httpStatus = http.StatusOK
		} else {
			response.Status = "failed"
		}
	}
	s.observeCompletion(
		domain.OperationAuthorizationIncrement,
		request.RequestID,
		response.Status,
		request.IncrementCents,
		started,
		result.Status.String(),
		"authorization_id", request.AuthorizationID,
		"card_id", original.DebitAccountID.String(),
	)
	return response, httpStatus, nil
}

func sameTransfer(left, right tb.Transfer) bool {
	return left.Timeout == right.Timeout && sameTransferExceptTimeout(left, right)
}

func sameTransferExceptTimeout(left, right tb.Transfer) bool {
	return left.ID == right.ID &&
		left.DebitAccountID == right.DebitAccountID &&
		left.CreditAccountID == right.CreditAccountID &&
		left.Amount == right.Amount &&
		left.PendingID == right.PendingID &&
		left.UserData128 == right.UserData128 &&
		left.UserData64 == right.UserData64 &&
		left.UserData32 == right.UserData32 &&
		left.Ledger == right.Ledger &&
		left.Code == right.Code &&
		left.Flags == right.Flags
}

func (s *Service) lookupExistingIncrement(
	request domain.IncrementAuthorizationRequest,
	original tb.Transfer,
	incrementID, authorizationID tb.Uint128,
	started time.Time,
	evaluationMS int64,
) (domain.IncrementAuthorizationResponse, int, bool, error) {
	existing, found, err := s.ledger.LookupTransfer(incrementID)
	if err != nil || !found {
		return domain.IncrementAuthorizationResponse{}, 0, false, err
	}
	expected := tb.Transfer{
		ID:              incrementID,
		DebitAccountID:  original.DebitAccountID,
		CreditAccountID: original.CreditAccountID,
		Amount:          tb.ToUint128(request.IncrementCents),
		UserData128:     authorizationID,
		UserData64:      original.UserData64,
		UserData32:      original.UserData32,
		Ledger:          s.ledgerID,
		Code:            ledger.TransferCodeAuthorizationIncrement,
		Flags:           tb.TransferFlags{Pending: true}.ToUint16(),
	}
	if !sameTransferExceptTimeout(existing, expected) {
		return domain.IncrementAuthorizationResponse{}, 0, true, domain.ErrIdempotencyConflict
	}
	response := domain.IncrementAuthorizationResponse{
		RequestID:        request.RequestID,
		AuthorizationID:  request.AuthorizationID,
		HoldTransferID:   request.RequestID,
		Status:           "approved",
		LedgerStatus:     tb.TransferExists.String(),
		IncrementCents:   request.IncrementCents,
		EvaluationTimeMS: evaluationMS,
		Timestamp:        existing.Timestamp,
	}
	s.observeCompletion(
		domain.OperationAuthorizationIncrement,
		request.RequestID,
		response.Status,
		request.IncrementCents,
		started,
		response.LedgerStatus,
		"authorization_id", request.AuthorizationID,
		"card_id", original.DebitAccountID.String(),
	)
	return response, http.StatusOK, true, nil
}

func incrementFailure(request domain.IncrementAuthorizationRequest, code, message string, status int, evaluationMS int64) (domain.IncrementAuthorizationResponse, int, error) {
	return domain.IncrementAuthorizationResponse{
		RequestID:        request.RequestID,
		AuthorizationID:  request.AuthorizationID,
		Status:           "declined",
		LedgerStatus:     "not_submitted",
		IncrementCents:   request.IncrementCents,
		EvaluationTimeMS: evaluationMS,
		ErrorCode:        code,
		Message:          message,
	}, status, nil
}

func (s *Service) observeCompletion(
	kind domain.OperationKind,
	requestID, status string,
	amountCents any,
	started time.Time,
	ledgerStatus string,
	attributes ...any,
) {
	elapsed := time.Since(started)
	if s.metrics != nil {
		s.metrics.ObserveOperation(string(kind), status, elapsed)
	}
	fields := []any{
		"kind", kind,
		"request_id", requestID,
		"transaction_id", requestID,
		"status", status,
		"ledger_status", ledgerStatus,
		"amount_cents", amountCents,
		"duration_ms", elapsed.Milliseconds(),
	}
	fields = append(fields, attributes...)
	s.logger.Info("ledger operation completed", fields...)
}
