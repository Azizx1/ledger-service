package ledger

import (
	"fmt"

	tb "github.com/tigerbeetle/tigerbeetle-go"
)

// Client adapts TigerBeetle's batch-shaped SDK to the service's single-command
// operations. Concurrent calls are still automatically batched by the SDK.
type Client struct {
	client tb.Client
}

func NewClient(clusterID tb.Uint128, addresses []string) (*Client, error) {
	client, err := tb.NewClient(clusterID, addresses)
	if err != nil {
		return nil, fmt.Errorf("initialize TigerBeetle client: %w", err)
	}
	return &Client{client: client}, nil
}

func (c *Client) CreateAccount(account tb.Account) (tb.CreateAccountResult, error) {
	results, err := c.client.CreateAccounts([]tb.Account{account})
	if err != nil {
		return tb.CreateAccountResult{}, fmt.Errorf("create account: %w", err)
	}
	if len(results) != 1 {
		return tb.CreateAccountResult{}, fmt.Errorf("create account: expected 1 result, received %d", len(results))
	}
	return results[0], nil
}

func (c *Client) CreateTransfer(transfer tb.Transfer) (tb.CreateTransferResult, error) {
	results, err := c.client.CreateTransfers([]tb.Transfer{transfer})
	if err != nil {
		return tb.CreateTransferResult{}, fmt.Errorf("create transfer: %w", err)
	}
	if len(results) != 1 {
		return tb.CreateTransferResult{}, fmt.Errorf("create transfer: expected 1 result, received %d", len(results))
	}
	return results[0], nil
}

func (c *Client) LookupAccount(id tb.Uint128) (tb.Account, bool, error) {
	accounts, err := c.client.LookupAccounts([]tb.Uint128{id})
	if err != nil {
		return tb.Account{}, false, fmt.Errorf("lookup account: %w", err)
	}
	if len(accounts) == 0 {
		return tb.Account{}, false, nil
	}
	if len(accounts) != 1 {
		return tb.Account{}, false, fmt.Errorf("lookup account: expected at most 1 account, received %d", len(accounts))
	}
	return accounts[0], true, nil
}

func (c *Client) LookupTransfer(id tb.Uint128) (tb.Transfer, bool, error) {
	transfers, err := c.client.LookupTransfers([]tb.Uint128{id})
	if err != nil {
		return tb.Transfer{}, false, fmt.Errorf("lookup transfer: %w", err)
	}
	if len(transfers) == 0 {
		return tb.Transfer{}, false, nil
	}
	if len(transfers) != 1 {
		return tb.Transfer{}, false, fmt.Errorf("lookup transfer: expected at most 1 transfer, received %d", len(transfers))
	}
	return transfers[0], true, nil
}

func (c *Client) Close() {
	c.client.Close()
}
