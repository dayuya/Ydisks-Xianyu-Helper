param(
    [ValidateSet('Start', 'Stop', 'Restart', 'Status', 'Logs')]
    [string]$Action = 'Start'
)

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot
docker info --format '{{.ServerVersion}}' | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Start Docker Desktop first.' }

switch ($Action) {
    'Start' { docker compose up -d --pull missing --wait --wait-timeout 240 }
    'Stop' { docker compose stop }
    'Restart' { docker compose restart }
    'Status' { docker compose ps }
    'Logs' { docker compose logs --tail 100 -f }
}
exit $LASTEXITCODE
