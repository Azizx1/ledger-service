# ADR 002: Model every funded card as a wallet account

Status: accepted

## Context

A corporate client funds one electronic-money wallet by bank transfer, then allocates portions of
those funds to multiple physical or virtual cards. Each card has a financial balance in addition
to policy controls such as daily limits, merchant categories, schedules, and geofences.

Treating all cards as access instruments for a single corporate account would preserve the total
client liability but lose the independently funded balance of each card. Treating a card limit as
money would create a shadow balance and could duplicate funds.

## Decision

Create one credit-normal corporate-wallet liability account per client and one credit-normal
card-wallet liability account per funded card. A card account stores its immutable parent
corporate-wallet ID in `Account.user_data_128`.

- A confirmed bank top-up debits safeguarded cash and credits the corporate wallet.
- Allocating funds debits the corporate wallet and credits a card wallet.
- Returning unused or expired funds debits the card wallet and credits the corporate wallet.
- An authorization debits the card wallet pending and credits the singleton card-settlement
  payable pending.

The card service stores the operational mapping from its card record to the card's TigerBeetle
account ID. The authorization request field remains named `card_id` to align with the challenge,
but its value is the card-wallet `Account.id`.

`merchant_id` is metadata and a risk input, not a TigerBeetle account. Open-loop card settlement
creates an obligation to the network, processor, or sponsor-bank settlement counterparty rather
than a direct payable to each merchant.

## Consequences

- Every card's posted and available balances are native TigerBeetle account balances.
- Allocations conserve the client's total liability; they move funds instead of copying them.
- Corporate and card account flags atomically prevent over-allocation, overspending, and returning
  funds already reserved by pending authorizations.
- Card policy can further restrict spending without becoming a second financial ledger.
- The corporate client's total funds are the unallocated corporate-wallet balance plus its card
  wallet balances. The card registry, not TigerBeetle Query APIs, supplies the authoritative list
  of a corporation's cards for aggregate reporting.
