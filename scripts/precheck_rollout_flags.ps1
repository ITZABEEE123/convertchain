param(
    [string]$TradeCreateEndpointMode = "warn",
    [string]$GraphWebhookEventIdMode = "warn"
)

$validTradeModes = @("allow", "warn", "enforce")
$validGraphModes = @("off", "warn", "enforce")

$tradeMode = $TradeCreateEndpointMode.Trim().ToLowerInvariant()
$graphMode = $GraphWebhookEventIdMode.Trim().ToLowerInvariant()

if ($validTradeModes -notcontains $tradeMode) {
    Write-Error "Invalid TRADE_CREATE_ENDPOINT_MODE: '$TradeCreateEndpointMode'. Allowed: allow, warn, enforce"
    exit 1
}

if ($validGraphModes -notcontains $graphMode) {
    Write-Error "Invalid GRAPH_WEBHOOK_EVENT_ID_MODE: '$GraphWebhookEventIdMode'. Allowed: off, warn, enforce"
    exit 1
}

Write-Host "Rollout flags validated"
Write-Host "TRADE_CREATE_ENDPOINT_MODE=$tradeMode"
Write-Host "GRAPH_WEBHOOK_EVENT_ID_MODE=$graphMode"

if ($tradeMode -eq "enforce") {
    Write-Warning "Legacy POST /api/v1/trades endpoint will be blocked (410)."
}

if ($graphMode -eq "enforce") {
    Write-Warning "Graph webhooks without event-id header will be rejected (400)."
}
