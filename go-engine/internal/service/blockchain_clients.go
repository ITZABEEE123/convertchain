package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"convert-chain/go-engine/internal/workers"
)

const (
	erc20TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
)

type ProductionBlockchainClient struct {
	logger     *slog.Logger
	httpClient *http.Client
	fallback   *SandboxBlockchainClient
	btcAdapter *BTCBlockstreamAdapter
	nativeEVM  map[string]*EVMNativeAdapter
	tokenEVM   map[string]map[string]*EVMTokenAdapter
}

func NewBlockchainClientFromEnv(logger *slog.Logger) workers.BlockchainClient {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("BLOCKCHAIN_MONITOR_MODE")))
	if mode == "" {
		mode = "sandbox"
	}

	if mode != "production" {
		logger.Info("using sandbox blockchain client", "mode", mode)
		return NewSandboxBlockchainClient()
	}

	client := &ProductionBlockchainClient{
		logger:     logger,
		httpClient: &http.Client{Timeout: 20 * time.Second},
		fallback:   NewSandboxBlockchainClient(),
		nativeEVM:  map[string]*EVMNativeAdapter{},
		tokenEVM:   map[string]map[string]*EVMTokenAdapter{},
	}

	btcBase := strings.TrimSpace(os.Getenv("BTC_BLOCKSTREAM_API_BASE_URL"))
	if btcBase == "" {
		btcBase = "https://blockstream.info/api"
	}
	client.btcAdapter = &BTCBlockstreamAdapter{baseURL: strings.TrimRight(btcBase, "/"), httpClient: client.httpClient}

	client.configureNativeEVM("ETH", "ethereum", "ETH_ETH_RPC_URL", "ETH_RPC_URL", "USDC_ETH_RPC_URL")
	client.configureNativeEVM("BNB", "bsc", "BNB_BSC_RPC_URL", "BSC_RPC_URL", "USDT_BSC_RPC_URL")

	client.configureTokenEVM("USDC", "ethereum", "USDC_ETH_RPC_URL", "ETH_RPC_URL", "0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", "USDC_ETH_CONTRACT")
	client.configureTokenEVM("USDC", "polygon", "USDC_POLYGON_RPC_URL", "POLYGON_RPC_URL", "0x3c499c542cef5e3811e1192ce70d8cc03d5c3359", "USDC_POLYGON_CONTRACT")
	client.configureTokenEVM("USDT", "ethereum", "USDT_ETH_RPC_URL", "ETH_RPC_URL", "0xdAC17F958D2ee523a2206206994597C13D831ec7", "USDT_ETH_CONTRACT")
	client.configureTokenEVM("USDT", "polygon", "USDT_POLYGON_RPC_URL", "POLYGON_RPC_URL", "0xc2132D05D31c914a87C6611C10748AEb04B58e8F", "USDT_POLYGON_CONTRACT")

	if len(client.tokenEVM) == 0 && len(client.nativeEVM) == 0 {
		logger.Warn("production blockchain monitor enabled but no EVM adapters configured")
	}
	logger.Info("using production blockchain monitor", "btc_adapter", client.btcAdapter != nil, "native_evm_networks", len(client.nativeEVM), "token_assets", len(client.tokenEVM))
	return client
}

func (c *ProductionBlockchainClient) CheckDeposit(ctx context.Context, currency string, address string, expectedAmount int64) (*workers.DepositResult, error) {
	trimmedAddress := strings.TrimSpace(address)
	if strings.HasPrefix(strings.ToLower(trimmedAddress), "sandbox://") {
		return c.fallback.CheckDeposit(ctx, currency, address, expectedAmount)
	}

	network, rawAddress := parseTaggedNetworkAddress(currency, trimmedAddress)
	currency = strings.ToUpper(strings.TrimSpace(currency))

	switch currency {
	case "BTC":
		if c.btcAdapter == nil {
			return nil, fmt.Errorf("btc adapter not configured")
		}
		result, err := c.btcAdapter.CheckAddressDeposit(ctx, rawAddress)
		if err != nil {
			return nil, err
		}
		result.Network = "btc"
		return result, nil
	case "ETH", "BNB":
		adapter := c.nativeEVM[network]
		if adapter == nil {
			return nil, fmt.Errorf("%s adapter not configured for network %s", strings.ToLower(currency), network)
		}
		result, err := adapter.CheckAddressDeposit(ctx, rawAddress)
		if err != nil {
			return nil, err
		}
		result.Network = adapter.network
		return result, nil
	case "USDC", "USDT":
		adapter := c.tokenAdapter(currency, network)
		if adapter == nil {
			return nil, fmt.Errorf("%s adapter not configured for network %s", strings.ToLower(currency), network)
		}
		result, err := adapter.CheckAddressDeposit(ctx, rawAddress)
		if err != nil {
			return nil, err
		}
		result.Network = adapter.network
		return result, nil
	default:
		return &workers.DepositResult{Found: false}, nil
	}
}

func (c *ProductionBlockchainClient) configureNativeEVM(asset, network string, rpcKeys ...string) {
	rpc := firstNonEmptyEnv(rpcKeys...)
	if rpc == "" {
		return
	}
	c.nativeEVM[normalizeNetworkName(network)] = &EVMNativeAdapter{
		asset:          strings.ToUpper(strings.TrimSpace(asset)),
		network:        normalizeNetworkName(network),
		rpcURL:         rpc,
		httpClient:     c.httpClient,
		lookbackBlocks: envInt64(strings.ToUpper(strings.TrimSpace(asset))+"_"+networkEnvPrefix(network)+"_DEPOSIT_LOOKBACK_BLOCKS", envInt64(strings.ToUpper(strings.TrimSpace(asset))+"_DEPOSIT_LOOKBACK_BLOCKS", envInt64("DEPOSIT_BACKFILL_LOOKBACK_BLOCKS", 9000))),
	}
}

func (c *ProductionBlockchainClient) configureTokenEVM(asset, network string, primaryRPCKey string, fallbackRPCKey string, defaultContract string, contractKey string) {
	rpc := firstNonEmptyEnv(primaryRPCKey, fallbackRPCKey)
	if rpc == "" {
		return
	}
	asset = strings.ToUpper(strings.TrimSpace(asset))
	network = normalizeNetworkName(network)
	contract := firstNonEmptyEnv(contractKey)
	if contract == "" {
		contract = defaultContract
	}
	if c.tokenEVM[asset] == nil {
		c.tokenEVM[asset] = map[string]*EVMTokenAdapter{}
	}
	c.tokenEVM[asset][network] = &EVMTokenAdapter{
		asset:           asset,
		network:         network,
		rpcURL:          rpc,
		contractAddress: strings.ToLower(contract),
		httpClient:      c.httpClient,
		lookbackBlocks:  envInt64(asset+"_"+networkEnvPrefix(network)+"_DEPOSIT_LOOKBACK_BLOCKS", envInt64("DEPOSIT_BACKFILL_LOOKBACK_BLOCKS", 9000)),
	}
}

func networkEnvPrefix(network string) string {
	switch normalizeNetworkName(network) {
	case "ethereum":
		return "ETH"
	case "polygon":
		return "POLYGON"
	case "bsc":
		return "BSC"
	case "btc":
		return "BTC"
	default:
		return strings.ToUpper(strings.TrimSpace(network))
	}
}

func (c *ProductionBlockchainClient) tokenAdapter(asset, network string) *EVMTokenAdapter {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	network = normalizeNetworkName(network)
	if c.tokenEVM[asset] == nil {
		return nil
	}
	if adapter := c.tokenEVM[asset][network]; adapter != nil {
		return adapter
	}
	return c.tokenEVM[asset]["ethereum"]
}

func parseTaggedNetworkAddress(currency, address string) (string, string) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	trimmed := strings.TrimSpace(address)
	if strings.Contains(trimmed, ":") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			network := normalizeNetworkName(parts[0])
			addressPart := strings.TrimSpace(parts[1])
			if network != "" && addressPart != "" {
				return network, addressPart
			}
		}
	}
	switch currency {
	case "BTC":
		return "btc", trimmed
	case "ETH":
		return "ethereum", trimmed
	case "BNB":
		return "bsc", trimmed
	case "USDC", "USDT":
		network := normalizeNetworkName(os.Getenv(currency + "_DEPOSIT_NETWORK"))
		if network == "" || network == "default" {
			network = "ethereum"
		}
		return network, trimmed
	default:
		return "default", trimmed
	}
}

func normalizeNetworkName(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "btc", "bitcoin", "mainnet", "bitcoin-mainnet":
		return "btc"
	case "eth", "ethereum", "erc20", "eth-mainnet":
		return "ethereum"
	case "polygon", "matic", "polygon-pos":
		return "polygon"
	case "bsc", "bnb", "bnb-smart-chain", "binance-smart-chain", "bep20":
		return "bsc"
	default:
		return value
	}
}

type BTCBlockstreamAdapter struct {
	baseURL    string
	httpClient *http.Client
}

type btcAddressTx struct {
	TxID   string `json:"txid"`
	Status struct {
		Confirmed   bool  `json:"confirmed"`
		BlockHeight int64 `json:"block_height"`
		BlockTime   int64 `json:"block_time"`
	} `json:"status"`
	Vout []struct {
		ScriptPubKeyAddress string `json:"scriptpubkey_address"`
		Value               int64  `json:"value"`
	} `json:"vout"`
}

func (b *BTCBlockstreamAdapter) CheckAddressDeposit(ctx context.Context, address string) (*workers.DepositResult, error) {
	safeAddress := url.PathEscape(strings.TrimSpace(address))
	if safeAddress == "" {
		return &workers.DepositResult{Found: false}, nil
	}

	txsURL := b.baseURL + "/address/" + safeAddress + "/txs"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, txsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("btc adapter address lookup failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var txs []btcAddressTx
	if err := json.NewDecoder(resp.Body).Decode(&txs); err != nil {
		return nil, err
	}
	if len(txs) == 0 {
		return &workers.DepositResult{Found: false, Network: "btc", Address: address}, nil
	}

	tipHeight, err := b.fetchTipHeight(ctx)
	if err != nil {
		return nil, err
	}

	best := pickBestBTCAddressTx(txs, strings.TrimSpace(address))
	if best == nil {
		return &workers.DepositResult{Found: false, Network: "btc", Address: address}, nil
	}

	confirmations := 0
	if best.Status.Confirmed && best.Status.BlockHeight > 0 {
		confirmations = int(tipHeight - best.Status.BlockHeight + 1)
		if confirmations < 0 {
			confirmations = 0
		}
	}

	amount := int64(0)
	for _, output := range best.Vout {
		if strings.EqualFold(strings.TrimSpace(output.ScriptPubKeyAddress), strings.TrimSpace(address)) {
			amount += output.Value
		}
	}

	return &workers.DepositResult{
		Found:          true,
		AmountReceived: amount,
		Confirmations:  confirmations,
		TxHash:         strings.TrimSpace(best.TxID),
		Network:        "btc",
		Address:        strings.TrimSpace(address),
		ReorgRisk:      confirmations == 0,
		Reversed:       false,
		Replaced:       false,
	}, nil
}

func pickBestBTCAddressTx(txs []btcAddressTx, address string) *btcAddressTx {
	var best *btcAddressTx
	for i := range txs {
		tx := &txs[i]
		match := false
		for _, output := range tx.Vout {
			if strings.EqualFold(strings.TrimSpace(output.ScriptPubKeyAddress), address) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		if best == nil {
			best = tx
			continue
		}
		if tx.Status.BlockHeight > best.Status.BlockHeight {
			best = tx
			continue
		}
		if tx.Status.BlockHeight == best.Status.BlockHeight && tx.Status.BlockTime > best.Status.BlockTime {
			best = tx
		}
	}
	return best
}

func (b *BTCBlockstreamAdapter) fetchTipHeight(ctx context.Context) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/blocks/tip/height", nil)
	if err != nil {
		return 0, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("btc adapter tip height failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

type EVMNativeAdapter struct {
	asset          string
	network        string
	rpcURL         string
	httpClient     *http.Client
	lookbackBlocks int64
}

type EVMTokenAdapter struct {
	asset           string
	network         string
	rpcURL          string
	contractAddress string
	httpClient      *http.Client
	lookbackBlocks  int64
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type evmLog struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	BlockNumber     string   `json:"blockNumber"`
	TransactionHash string   `json:"transactionHash"`
	Removed         bool     `json:"removed"`
}

type evmBlock struct {
	Number       string `json:"number"`
	Transactions []struct {
		Hash  string `json:"hash"`
		To    string `json:"to"`
		Value string `json:"value"`
	} `json:"transactions"`
}

func (a *EVMNativeAdapter) CheckAddressDeposit(ctx context.Context, address string) (*workers.DepositResult, error) {
	normalizedAddress := strings.ToLower(strings.TrimSpace(address))
	if normalizedAddress == "" {
		return &workers.DepositResult{Found: false, Network: a.network}, nil
	}
	if !strings.HasPrefix(normalizedAddress, "0x") {
		normalizedAddress = "0x" + normalizedAddress
	}

	latestBlock, err := evmBlockNumber(ctx, a.rpcURL, a.httpClient)
	if err != nil {
		return nil, err
	}
	fromBlock := latestBlock - a.lookbackBlocks
	if fromBlock < 0 {
		fromBlock = 0
	}

	var bestHash string
	var bestAmount *big.Int
	bestBlock := int64(-1)
	for blockNumber := latestBlock; blockNumber >= fromBlock; blockNumber-- {
		var block evmBlock
		if err := evmCallRPC(ctx, a.rpcURL, a.httpClient, "eth_getBlockByNumber", []interface{}{fmt.Sprintf("0x%x", blockNumber), true}, &block); err != nil {
			return nil, err
		}
		for _, tx := range block.Transactions {
			if !strings.EqualFold(strings.TrimSpace(tx.To), normalizedAddress) {
				continue
			}
			amount := hexToBigInt(tx.Value)
			if amount.Sign() <= 0 {
				continue
			}
			bestHash = strings.TrimSpace(tx.Hash)
			bestAmount = amount
			bestBlock = blockNumber
			break
		}
		if bestHash != "" {
			break
		}
	}

	if bestHash == "" || bestAmount == nil {
		return &workers.DepositResult{Found: false, Network: a.network, Address: normalizedAddress}, nil
	}
	if !bestAmount.IsInt64() {
		return nil, fmt.Errorf("%s amount overflow for tx %s", strings.ToLower(a.asset), bestHash)
	}
	confirmations := int(latestBlock - bestBlock + 1)
	if confirmations < 0 {
		confirmations = 0
	}

	return &workers.DepositResult{
		Found:          true,
		AmountReceived: bestAmount.Int64(),
		Confirmations:  confirmations,
		TxHash:         bestHash,
		Network:        a.network,
		Address:        normalizedAddress,
		ReorgRisk:      confirmations == 0,
		Reversed:       false,
		Replaced:       false,
	}, nil
}

func (a *EVMTokenAdapter) CheckAddressDeposit(ctx context.Context, address string) (*workers.DepositResult, error) {
	normalizedAddress := strings.ToLower(strings.TrimSpace(address))
	if normalizedAddress == "" {
		return &workers.DepositResult{Found: false, Network: a.network}, nil
	}
	if !strings.HasPrefix(normalizedAddress, "0x") {
		normalizedAddress = "0x" + normalizedAddress
	}

	latestBlock, err := evmBlockNumber(ctx, a.rpcURL, a.httpClient)
	if err != nil {
		return nil, err
	}
	fromBlock := latestBlock - a.lookbackBlocks
	if fromBlock < 0 {
		fromBlock = 0
	}

	topicTo := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(normalizedAddress, "0x")
	params := []interface{}{map[string]interface{}{
		"fromBlock": fmt.Sprintf("0x%x", fromBlock),
		"toBlock":   fmt.Sprintf("0x%x", latestBlock),
		"address":   a.contractAddress,
		"topics":    []interface{}{erc20TransferTopic, nil, strings.ToLower(topicTo)},
	}}

	var logs []evmLog
	if err := evmCallRPC(ctx, a.rpcURL, a.httpClient, "eth_getLogs", params, &logs); err != nil {
		return nil, err
	}
	if len(logs) == 0 {
		return &workers.DepositResult{Found: false, Network: a.network, Address: normalizedAddress}, nil
	}

	best := logs[len(logs)-1]
	bestBlock := hexToInt64(best.BlockNumber)
	for _, entry := range logs {
		entryBlock := hexToInt64(entry.BlockNumber)
		if entryBlock >= bestBlock {
			best = entry
			bestBlock = entryBlock
		}
	}

	confirmations := int(latestBlock - bestBlock + 1)
	if confirmations < 0 {
		confirmations = 0
	}

	amount := hexToBigInt(best.Data)
	if !amount.IsInt64() {
		return nil, fmt.Errorf("%s amount overflow for tx %s", strings.ToLower(a.asset), best.TransactionHash)
	}

	return &workers.DepositResult{
		Found:          true,
		AmountReceived: amount.Int64(),
		Confirmations:  confirmations,
		TxHash:         strings.TrimSpace(best.TransactionHash),
		Network:        a.network,
		Address:        normalizedAddress,
		ReorgRisk:      best.Removed || confirmations == 0,
		Reversed:       best.Removed,
		Replaced:       false,
	}, nil
}

func evmBlockNumber(ctx context.Context, rpcURL string, httpClient *http.Client) (int64, error) {
	var blockHex string
	if err := evmCallRPC(ctx, rpcURL, httpClient, "eth_blockNumber", []interface{}{}, &blockHex); err != nil {
		return 0, err
	}
	return hexToInt64(blockHex), nil
}

func evmCallRPC(ctx context.Context, rpcURL string, httpClient *http.Client, method string, params interface{}, result interface{}) error {
	payload := rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("evm rpc http error: status=%d body=%s", resp.StatusCode, string(message))
	}

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return err
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("evm rpc error method=%s code=%d message=%s", method, rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if len(rpcResp.Result) == 0 {
		return fmt.Errorf("evm rpc empty result for method %s", method)
	}
	return json.Unmarshal(rpcResp.Result, result)
}

func hexToInt64(value string) int64 {
	trimmed := strings.TrimSpace(strings.TrimPrefix(value, "0x"))
	if trimmed == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(trimmed, 16, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func hexToBigInt(value string) *big.Int {
	result := new(big.Int)
	trimmed := strings.TrimSpace(strings.TrimPrefix(value, "0x"))
	if trimmed == "" {
		return result
	}
	result.SetString(trimmed, 16)
	return result
}

func envInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
