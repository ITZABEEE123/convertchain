param(
    [string]$EnvFile = "../.env",
    [string]$VaultContainer = "convertchain-vault"
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$envPath = Join-Path $scriptDir $EnvFile
$envPath = [System.IO.Path]::GetFullPath($envPath)

if (-not (Test-Path $envPath)) {
    throw "Missing env file: $envPath"
}

function Read-EnvFile {
    param([string]$Path)

    $vars = @{}
    Get-Content -Path $Path | ForEach-Object {
        $line = $_.Trim()
        if (-not $line -or $line.StartsWith("#")) {
            return
        }

        $eqIndex = $line.IndexOf("=")
        if ($eqIndex -le 0) {
            return
        }

        $key = $line.Substring(0, $eqIndex).Trim()
        $value = $line.Substring($eqIndex + 1).Trim()
        $vars[$key] = $value
    }

    return $vars
}

function Vault-KvPut {
    param(
        [string]$Token,
        [string]$Addr,
        [string]$Container,
        [string]$Path,
        [string[]]$KvPairs
    )

    $args = @("exec", "-e", "VAULT_ADDR=$Addr", "-e", "VAULT_TOKEN=$Token", $Container, "vault", "kv", "put", $Path) + $KvPairs
    & docker @args | Out-Null
}

$running = (& docker ps --format "{{.Names}}") -split "`n"
if ($running -notcontains $VaultContainer) {
    throw "Vault container '$VaultContainer' is not running. Run: docker compose up -d vault"
}

$cfg = Read-EnvFile -Path $envPath
$vaultToken = if ($cfg.ContainsKey("VAULT_TOKEN")) { $cfg["VAULT_TOKEN"] } else { "devroot" }
$vaultAddr = if ($cfg.ContainsKey("VAULT_ADDR") -and $cfg["VAULT_ADDR"]) { $cfg["VAULT_ADDR"] } else { "http://127.0.0.1:8200" }
$serviceToken = if ($cfg.ContainsKey("SERVICE_TOKEN")) { $cfg["SERVICE_TOKEN"] } else { "convertchain-dev-service-token-change-me" }

$piiKeyHex = if ($cfg.ContainsKey("PII_KEY_HEX") -and $cfg["PII_KEY_HEX"]) {
    $cfg["PII_KEY_HEX"]
} else {
    # 32 random bytes encoded as 64-char hex.
    -join ((1..64) | ForEach-Object { "0123456789abcdef"[(Get-Random -Minimum 0 -Maximum 16)] })
}

Write-Host "[INFO] Writing required secrets to Vault..."

Vault-KvPut -Token $vaultToken -Addr $vaultAddr -Container $VaultContainer -Path "secret/convertchain/binance" -KvPairs @(
    "api_key=$($cfg["BINANCE_API_KEY"])",
    "api_secret=$($cfg["BINANCE_SECRET_KEY"] )"
)

Vault-KvPut -Token $vaultToken -Addr $vaultAddr -Container $VaultContainer -Path "secret/convertchain/bybit" -KvPairs @(
    "api_key=$($cfg["BYBIT_API_KEY"])",
    "api_secret=$($cfg["BYBIT_SECRET_KEY"] )"
)

Vault-KvPut -Token $vaultToken -Addr $vaultAddr -Container $VaultContainer -Path "secret/convertchain/graph" -KvPairs @(
    "api_key=$($cfg["GRAPH_API_KEY"] )"
)

Vault-KvPut -Token $vaultToken -Addr $vaultAddr -Container $VaultContainer -Path "secret/convertchain/smileid" -KvPairs @(
    "partner_id=$($cfg["SMILE_ID_PARTNER_ID"])",
    "api_key=$($cfg["SMILE_ID_API_KEY"] )"
)

Vault-KvPut -Token $vaultToken -Addr $vaultAddr -Container $VaultContainer -Path "secret/convertchain/pii_key" -KvPairs @(
    "key=$piiKeyHex"
)

Vault-KvPut -Token $vaultToken -Addr $vaultAddr -Container $VaultContainer -Path "secret/convertchain/service_token" -KvPairs @(
    "token=$serviceToken"
)

if (($cfg["SUMSUB_APP_TOKEN"]) -or ($cfg["SUMSUB_SECRET_KEY"])) {
    Vault-KvPut -Token $vaultToken -Addr $vaultAddr -Container $VaultContainer -Path "secret/convertchain/sumsub" -KvPairs @(
        "app_token=$($cfg["SUMSUB_APP_TOKEN"])",
        "secret_key=$($cfg["SUMSUB_SECRET_KEY"] )"
    )
}

Write-Host "[INFO] Verifying required paths..."
$paths = @(
    "convertchain/binance",
    "convertchain/bybit",
    "convertchain/graph",
    "convertchain/smileid",
    "convertchain/pii_key",
    "convertchain/service_token"
)

foreach ($path in $paths) {
    & docker exec -e "VAULT_ADDR=$vaultAddr" -e "VAULT_TOKEN=$vaultToken" $VaultContainer vault kv get "secret/$path" | Out-Null
    Write-Host "  - ok: secret/$path"
}

Write-Host "[DONE] Vault bootstrap complete."
