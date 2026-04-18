package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"convert-chain/go-engine/internal/workers"
)

type MockBlockchainClient struct {
	mu       sync.Mutex
	checksBy map[string]int
}

func NewMockBlockchainClient() *MockBlockchainClient {
	return &MockBlockchainClient{
		checksBy: make(map[string]int),
	}
}

func (m *MockBlockchainClient) CheckDeposit(_ context.Context, currency string, address string, expectedAmount int64) (*workers.DepositResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.checksBy[address]++
	checks := m.checksBy[address]

	switch {
	case checks == 1:
		return &workers.DepositResult{Found: false}, nil
	case checks == 2:
		return &workers.DepositResult{
			Found:          true,
			AmountReceived: expectedAmount,
			Confirmations:  1,
			TxHash:         fmt.Sprintf("mock_%s_%s", currency, address),
		}, nil
	default:
		return &workers.DepositResult{
			Found:          true,
			AmountReceived: expectedAmount,
			Confirmations:  3,
			TxHash:         fmt.Sprintf("mock_%s_%s", currency, address),
		}, nil
	}
}

type MockGraphFinanceClient struct{}

func NewMockGraphFinanceClient() *MockGraphFinanceClient {
	return &MockGraphFinanceClient{}
}

func (m *MockGraphFinanceClient) ConvertAndPay(_ context.Context, bankAccountID string, payoutAmount int64) (string, error) {
	return fmt.Sprintf("mock_payout_%s_%d_%d", bankAccountID, payoutAmount, time.Now().Unix()), nil
}
