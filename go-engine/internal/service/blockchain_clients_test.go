package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSandboxBlockchainClientProgressionIncludesNetworkAndAddress(t *testing.T) {
	client := NewSandboxBlockchainClient()
	address := "sandbox://deposit/btc/trd-test"

	first, err := client.CheckDeposit(context.Background(), "BTC", address, 100)
	if err != nil {
		t.Fatalf("first check failed: %v", err)
	}
	if first.Found {
		t.Fatalf("expected first sandbox check to have no deposit")
	}

	second, err := client.CheckDeposit(context.Background(), "BTC", address, 100)
	if err != nil {
		t.Fatalf("second check failed: %v", err)
	}
	if !second.Found || second.Confirmations != 1 {
		t.Fatalf("expected second check to find one confirmation, got %#v", second)
	}
	if second.Network != "sandbox" || second.Address != address {
		t.Fatalf("expected sandbox network/address metadata, got network=%q address=%q", second.Network, second.Address)
	}

	third, err := client.CheckDeposit(context.Background(), "BTC", address, 100)
	if err != nil {
		t.Fatalf("third check failed: %v", err)
	}
	if !third.Found || third.Confirmations != 2 {
		t.Fatalf("expected third check to reach BTC sandbox finality, got %#v", third)
	}
}

func TestBTCBlockstreamAdapterParsesConfirmedDeposit(t *testing.T) {
	address := "bc1qtestaddress"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/blocks/tip/height":
			_, _ = w.Write([]byte("101"))
		case r.URL.Path == "/address/"+address+"/txs":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"txid": "btc_tx_1",
					"status": map[string]interface{}{
						"confirmed":    true,
						"block_height": 100,
						"block_time":   1777076000,
					},
					"vout": []map[string]interface{}{
						{"scriptpubkey_address": address, "value": 25000000},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := &BTCBlockstreamAdapter{baseURL: server.URL, httpClient: server.Client()}
	result, err := adapter.CheckAddressDeposit(context.Background(), address)
	if err != nil {
		t.Fatalf("BTC check failed: %v", err)
	}
	if !result.Found || result.TxHash != "btc_tx_1" || result.AmountReceived != 25000000 || result.Confirmations != 2 {
		t.Fatalf("unexpected BTC result: %#v", result)
	}
	if result.Network != "btc" || result.Address != address || result.ReorgRisk {
		t.Fatalf("unexpected BTC metadata: %#v", result)
	}
}

func TestEVMUSDCAdapterParsesTransferLog(t *testing.T) {
	const address = "0x1111111111111111111111111111111111111111"
	const contract = "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"
	const amountHex = "0x17d7840"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		switch req.Method {
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`"0x64"`)})
		case "eth_getLogs":
			toTopic := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(address), "0x")
			logs := []evmLog{{
				Address:         contract,
				Topics:          []string{erc20TransferTopic, "0xfrom", toTopic},
				Data:            amountHex,
				BlockNumber:     "0x60",
				TransactionHash: "0xusdc_tx_1",
				Removed:         false,
			}}
			body, _ := json.Marshal(logs)
			_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: body})
		default:
			http.Error(w, fmt.Sprintf("unexpected method %s", req.Method), http.StatusBadRequest)
		}
	}))
	defer server.Close()

	adapter := &EVMUSDCAdapter{
		network:         "ethereum",
		rpcURL:          server.URL,
		contractAddress: contract,
		httpClient:      server.Client(),
		lookbackBlocks:  10,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := adapter.CheckAddressDeposit(ctx, address)
	if err != nil {
		t.Fatalf("USDC check failed: %v", err)
	}
	if !result.Found || result.TxHash != "0xusdc_tx_1" || result.AmountReceived != 25000000 || result.Confirmations != 5 {
		t.Fatalf("unexpected USDC result: %#v", result)
	}
	if result.Network != "ethereum" || result.Address != strings.ToLower(address) || result.ReorgRisk {
		t.Fatalf("unexpected USDC metadata: %#v", result)
	}
}
