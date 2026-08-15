package ledger

import (
	"testing"

	"github.com/abdulaziz/ledger-service/internal/domain"
	tb "github.com/tigerbeetle/tigerbeetle-go"
)

func TestAccountModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind                  domain.AccountKind
		code                  uint16
		requiresParent        bool
		debitsCannotOverdraw  bool
		creditsCannotOverdraw bool
	}{
		{domain.AccountSafeguardedCash, AccountCodeSafeguardedCash, false, false, true},
		{domain.AccountCorporateWallet, AccountCodeCorporateWallet, false, true, false},
		{domain.AccountCardWallet, AccountCodeCardWallet, true, true, false},
		{domain.AccountCardSettlementPayable, AccountCodeCardSettlementPayable, false, true, false},
	}

	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			id := tb.ID()
			parentID := tb.Uint128{}
			if test.requiresParent {
				parentID = tb.ID()
			}
			account, err := Account(test.kind, id, parentID, 7)
			if err != nil {
				t.Fatal(err)
			}
			if account.ID != id || account.Ledger != 7 || account.Code != test.code {
				t.Fatalf("unexpected ledger/code: %d/%d", account.Ledger, account.Code)
			}
			if account.UserData128 != parentID {
				t.Fatalf("unexpected corporate parent: %s", account.UserData128.String())
			}
			flags := account.AccountFlags()
			if flags.DebitsMustNotExceedCredits != test.debitsCannotOverdraw {
				t.Fatalf("unexpected debit constraint: %v", flags.DebitsMustNotExceedCredits)
			}
			if flags.CreditsMustNotExceedDebits != test.creditsCannotOverdraw {
				t.Fatalf("unexpected credit constraint: %v", flags.CreditsMustNotExceedDebits)
			}
		})
	}
}

func TestSystemAccountIDsAreReservedAndStable(t *testing.T) {
	t.Parallel()

	cash := SafeguardedCashAccountID()
	settlement := CardSettlementPayableAccountID()
	if cash != SafeguardedCashAccountID() || settlement != CardSettlementPayableAccountID() {
		t.Fatal("system account IDs are not stable")
	}
	if cash.String() == "0" || settlement.String() == "0" || cash == settlement {
		t.Fatal("system account IDs must be distinct and non-zero")
	}
	if !IsSystemAccountID(cash) || !IsSystemAccountID(settlement) || IsSystemAccountID(tb.ID()) {
		t.Fatal("system account ID classification is incorrect")
	}
}
