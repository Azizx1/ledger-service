package ledger

import (
	"fmt"

	"github.com/Azizx1/ledger-service/internal/domain"
	tb "github.com/tigerbeetle/tigerbeetle-go"
)

const (
	AccountCodeSafeguardedCash       uint16 = 1001
	AccountCodeCorporateWallet       uint16 = 2001
	AccountCodeCardWallet            uint16 = 2002
	AccountCodeCardSettlementPayable uint16 = 2003

	TransferCodeTopUp                  uint16 = 1001
	TransferCodeWithdrawal             uint16 = 1002
	TransferCodeCardAllocation         uint16 = 1201
	TransferCodeCardReturn             uint16 = 1202
	TransferCodeAuthorization          uint16 = 1101
	TransferCodeAuthorizationIncrement uint16 = 1102
)

func SafeguardedCashAccountID() tb.Uint128 {
	// Account IDs 1 and 2 are reserved by this service for singleton system accounts.
	// Public account IDs are caller-generated time-based TigerBeetle IDs, so they
	// do not use this reserved application range.
	return tb.ToUint128(1)
}

func CardSettlementPayableAccountID() tb.Uint128 {
	return tb.ToUint128(2)
}

func IsSystemAccountID(id tb.Uint128) bool {
	return id == SafeguardedCashAccountID() || id == CardSettlementPayableAccountID()
}

func Account(kind domain.AccountKind, id, corporateAccountID tb.Uint128, ledgerID uint32) (tb.Account, error) {
	if !kind.Valid() {
		return tb.Account{}, fmt.Errorf("unsupported account kind %q", kind)
	}
	if id == (tb.Uint128{}) {
		return tb.Account{}, fmt.Errorf("account id must be non-zero")
	}
	if ledgerID == 0 {
		return tb.Account{}, fmt.Errorf("ledger id must be non-zero")
	}

	account := tb.Account{
		ID:          id,
		UserData128: corporateAccountID,
		Ledger:      ledgerID,
	}

	switch kind {
	case domain.AccountSafeguardedCash:
		if corporateAccountID != (tb.Uint128{}) {
			return tb.Account{}, fmt.Errorf("safeguarded cash cannot have a corporate parent")
		}
		account.Code = AccountCodeSafeguardedCash
		account.Flags = tb.AccountFlags{CreditsMustNotExceedDebits: true}.ToUint16()
	case domain.AccountCorporateWallet:
		if corporateAccountID != (tb.Uint128{}) {
			return tb.Account{}, fmt.Errorf("corporate wallet cannot have a corporate parent")
		}
		account.Code = AccountCodeCorporateWallet
		account.Flags = tb.AccountFlags{DebitsMustNotExceedCredits: true}.ToUint16()
	case domain.AccountCardWallet:
		if corporateAccountID == (tb.Uint128{}) {
			return tb.Account{}, fmt.Errorf("card wallet requires a corporate parent")
		}
		if corporateAccountID == id {
			return tb.Account{}, fmt.Errorf("card wallet cannot be its own corporate parent")
		}
		account.Code = AccountCodeCardWallet
		account.Flags = tb.AccountFlags{DebitsMustNotExceedCredits: true}.ToUint16()
	case domain.AccountCardSettlementPayable:
		if corporateAccountID != (tb.Uint128{}) {
			return tb.Account{}, fmt.Errorf("card settlement payable cannot have a corporate parent")
		}
		account.Code = AccountCodeCardSettlementPayable
		account.Flags = tb.AccountFlags{DebitsMustNotExceedCredits: true}.ToUint16()
	}

	return account, nil
}
