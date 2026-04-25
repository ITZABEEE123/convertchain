param(
    [string]$ComposeFile = "docker-compose.yml"
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

docker compose -f $ComposeFile run --rm migrate
