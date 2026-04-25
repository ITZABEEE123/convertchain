param(
    [string]$EngineUrl = "http://localhost:9000",
    [string]$ServiceToken = $env:SERVICE_TOKEN,
    [string]$TransactionPassword = "123456",
    [int]$TimeoutSeconds = 90
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot

function Get-DotEnvValue {
    param(
        [Parameter(Mandatory = $true)] [string]$Key
    )

    $envPath = Join-Path $repoRoot ".env"
    if (-not (Test-Path $envPath)) {
        return ""
    }

    $line = Get-Content -LiteralPath $envPath |
        Where-Object { $_ -match "^\s*$([regex]::Escape($Key))=" } |
        Select-Object -Last 1
    if (-not $line) {
        return ""
    }

    $value = $line.Substring($line.IndexOf("=") + 1).Trim()
    return $value.Trim('"').Trim("'")
}

if ([string]::IsNullOrWhiteSpace($ServiceToken)) {
    $ServiceToken = Get-DotEnvValue "SERVICE_TOKEN"
}
if ([string]::IsNullOrWhiteSpace($ServiceToken)) {
    $ServiceToken = "dev-service-token"
}

$headers = @{
    "X-Service-Token" = $ServiceToken
}

function ConvertTo-BodyJson {
    param([Parameter(Mandatory = $true)] [object]$Body)
    return ($Body | ConvertTo-Json -Depth 20 -Compress)
}

function Invoke-EngineJson {
    param(
        [Parameter(Mandatory = $true)] [string]$Method,
        [Parameter(Mandatory = $true)] [string]$Path,
        [object]$Body = $null
    )

    $uri = "$EngineUrl$Path"
    try {
        if ($null -eq $Body) {
            return Invoke-RestMethod -Method $Method -Uri $uri -Headers $headers
        }

        return Invoke-RestMethod `
            -Method $Method `
            -Uri $uri `
            -Headers $headers `
            -ContentType "application/json" `
            -Body (ConvertTo-BodyJson $Body)
    } catch {
        $response = $_.Exception.Response
        if ($response -and $response.GetResponseStream()) {
            $reader = [System.IO.StreamReader]::new($response.GetResponseStream())
            $payload = $reader.ReadToEnd()
            throw "Request $Method $Path failed: $payload"
        }
        throw
    }
}

function Wait-Engine {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            Invoke-RestMethod -Method GET -Uri "$EngineUrl/health" | Out-Null
            return
        } catch {
            Start-Sleep -Seconds 2
        }
    }
    throw "Go engine did not become healthy at $EngineUrl within $TimeoutSeconds seconds."
}

function Format-Rfc3339Utc {
    return (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
}

Wait-Engine

$stamp = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$channelUserID = "smoke-$stamp"

Write-Host "Creating sandbox user $channelUserID"
$user = Invoke-EngineJson POST "/api/v1/users" @{
    channel_type = "telegram"
    channel_user_id = $channelUserID
    username = "local_smoke"
    phone_number = "08000000001"
    locale = "en-NG"
}
$userID = $user.user_id

Write-Host "Recording consent"
Invoke-EngineJson POST "/api/v1/consent" @{
    user_id = $userID
    consent_version = "local-smoke-v1"
    consented_at = Format-Rfc3339Utc
} | Out-Null

Write-Host "Submitting auto-approved Tier 1 KYC"
$kyc = Invoke-EngineJson POST "/api/v1/kyc/submit" @{
    user_id = $userID
    first_name = "Smoke"
    last_name = "Tester"
    date_of_birth = "1990-01-01"
    phone_number = "08000000001"
    nin = "12345678901"
    bvn = "10987654321"
    tier = "TIER_1"
}
if ($kyc.status -ne "APPROVED") {
    throw "Expected KYC APPROVED, got '$($kyc.status)'. Ensure AUTO_APPROVE_KYC=true for local smoke tests."
}

Write-Host "Setting transaction password"
Invoke-EngineJson POST "/api/v1/security/transaction-password/setup" @{
    user_id = $userID
    transaction_password = $TransactionPassword
    confirm_password = $TransactionPassword
} | Out-Null

Write-Host "Adding sandbox payout bank account"
$bankAccount = Invoke-EngineJson POST "/api/v1/bank-accounts" @{
    user_id = $userID
    bank_code = "000000"
    account_number = "0000000001"
    account_name = "Sandbox Test Account"
}
$bankAccountID = $bankAccount.bank_account_id

Write-Host "Creating sandbox USDT quote"
$quote = Invoke-EngineJson POST "/api/v1/quotes" @{
    user_id = $userID
    asset = "USDT"
    amount = "25"
    direction = "sell"
}
$quoteID = $quote.quote_id

Write-Host "Confirming trade"
$trade = Invoke-EngineJson POST "/api/v1/trades/confirm" @{
    user_id = $userID
    quote_id = $quoteID
    bank_account_id = $bankAccountID
    transaction_password = $TransactionPassword
}
$tradeID = $trade.trade_id

Write-Host "Polling trade lifecycle for payout completion"
$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
$terminalFailureStatuses = @("PAYOUT_FAILED", "DISPUTE", "CANCELLED")
do {
    $trade = Invoke-EngineJson GET "/api/v1/trades/$tradeID"
    Write-Host "Trade $tradeID status: $($trade.status)"

    if ($trade.status -eq "PAYOUT_COMPLETED") {
        $summary = [ordered]@{
            user_id = $userID
            bank_account_id = $bankAccountID
            quote_id = $quoteID
            trade_id = $tradeID
            trade_ref = $trade.trade_ref
            final_status = $trade.status
            net_amount_kobo = $trade.net_amount_kobo
            payout_ref = $trade.payout_ref
        }
        $summary | ConvertTo-Json -Depth 10
        exit 0
    }

    if ($terminalFailureStatuses -contains $trade.status) {
        throw "Trade reached terminal failure status '$($trade.status)'."
    }

    Start-Sleep -Seconds 5
} while ((Get-Date) -lt $deadline)

throw "Timed out waiting for payout completion. Last status: $($trade.status)"
