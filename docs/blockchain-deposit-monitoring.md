# Blockchain Deposit Monitoring

ConvertChain supports two deposit-monitoring modes:

- `BLOCKCHAIN_MONITOR_MODE=sandbox`: local deterministic sandbox client used by Docker Compose and smoke tests.
- `BLOCKCHAIN_MONITOR_MODE=production`: public-chain adapters for BTC and configured USDC networks.

Production mode currently uses:

- BTC: Blockstream-compatible address transaction API.
- USDC Ethereum/Polygon: JSON-RPC `eth_getLogs` over the configured USDC contract.

## Configuration

BTC:

```env
BTC_BLOCKSTREAM_API_BASE_URL=https://blockstream.info/api
BTC_DEPOSIT_ADDRESS=
BTC_DEPOSIT_DETECTION_CONFIRMATIONS=1
BTC_DEPOSIT_FINALITY_CONFIRMATIONS=2
BTC_DEPOSIT_AMOUNT_TOLERANCE_MINOR=0
```

USDC Ethereum:

```env
USDC_DEPOSIT_NETWORK=ethereum
USDC_ETH_DEPOSIT_ADDRESS=
USDC_ETH_RPC_URL=
USDC_ETH_CONTRACT=0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48
USDC_ETH_DEPOSIT_DETECTION_CONFIRMATIONS=1
USDC_ETH_DEPOSIT_FINALITY_CONFIRMATIONS=12
USDC_ETH_DEPOSIT_AMOUNT_TOLERANCE_MINOR=0
```

USDC Polygon:

```env
USDC_POLYGON_DEPOSIT_ADDRESS=
USDC_POLYGON_RPC_URL=
USDC_POLYGON_CONTRACT=0x3c499c542cef5e3811e1192ce70d8cc03d5c3359
USDC_POLYGON_DEPOSIT_DETECTION_CONFIRMATIONS=1
USDC_POLYGON_DEPOSIT_FINALITY_CONFIRMATIONS=64
USDC_POLYGON_DEPOSIT_AMOUNT_TOLERANCE_MINOR=0
```

Backfill:

```env
DEPOSIT_BACKFILL_LOOKBACK_BLOCKS=9000
```

## Matching Rules

The watcher and backfill scanner validate:

- Currency and configured network.
- Destination address when the adapter reports one.
- Amount within configured minor-unit tolerance.
- Unique transaction hash across trades.
- Confirmation depth against detection/finality policy.
- Reorg or replacement flags.

Wrong amount, wrong network, wrong address, duplicate transaction hash, or reorg/replacement detection moves the trade to `DISPUTE` for manual review instead of payout.

## Address Safety

Do not configure private keys in environment variables or repo files. Production trade creation refuses to generate sandbox addresses and requires provider-managed BTC or USDC deposit addresses to be configured. For USDC, persisted deposit addresses are tagged as `network:address` so the watcher can enforce the correct chain.

The configured address values should come from a provider-managed deposit address service or a Vault/HSM-backed wallet allocation workflow. The Go engine fails startup if common private-key environment variables are present.
