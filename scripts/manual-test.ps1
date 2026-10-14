param(
    [string]$BaseUrl = 'http://localhost:8080',
    [int]$TimeoutSeconds = 90
)

$ErrorActionPreference = 'Stop'

function Invoke-Api([string]$Method, [string]$Path, $Body = $null) {
    $params = @{ Method = $Method; Uri = "$BaseUrl$Path"; UseBasicParsing = $true; ContentType = 'application/json' }
    if ($null -ne $Body) { $params.Body = ($Body | ConvertTo-Json -Compress) }
    try {
        $r = Invoke-WebRequest @params
        return [pscustomobject]@{ Status = [int]$r.StatusCode; Body = $r.Content }
    } catch {
        $resp = $_.Exception.Response
        if ($null -eq $resp) { throw }
        return [pscustomobject]@{ Status = [int]$resp.StatusCode; Body = $_.ErrorDetails.Message }
    }
}

function To-Cents($value) { [int64][math]::Round([double]$value * 100) }

function Format-Money([int64]$cents) { '{0}.{1:D2}' -f [math]::Floor($cents / 100), ($cents % 100) }

function Step([string]$text) { Write-Host "`n== $text" -ForegroundColor Cyan }

$failures = 0
function Check([bool]$ok, [string]$text) {
    if ($ok) { Write-Host "  [PASS] $text" -ForegroundColor Green }
    else { Write-Host "  [FAIL] $text" -ForegroundColor Red; $script:failures++ }
}

Step "1. Health check ($BaseUrl)"
$health = Invoke-Api GET /health
Check ($health.Status -eq 200) "GET /health -> $($health.Status) $($health.Body)"
if ($health.Status -ne 200) { exit 1 }

Step '2. Create a test round'
$created = Invoke-Api POST /rounds/test
Check ($created.Status -eq 201) "POST /rounds/test -> $($created.Status)"
$round = $created.Body | ConvertFrom-Json
Write-Host "  round $($round.roundId), state $($round.state), betting window $($round.bettingWindowSeconds)s"

$plan = @(
    @{ userId = 'user-1'; multiplier = 1.2; amount = 10 },
    @{ userId = 'user-2'; multiplier = 1.5; amount = 10 },
    @{ userId = 'user-3'; multiplier = 2; amount = 10 },
    @{ userId = 'user-4'; multiplier = 3; amount = 10 },
    @{ userId = 'user-5'; multiplier = 5; amount = 10 }
)

Step '3. Place bets'
$before = @{}
$accepted = @()
foreach ($p in $plan) {
    $before[$p.userId] = To-Cents ((Invoke-Api GET "/players/$($p.userId)/balance").Body | ConvertFrom-Json).balance
    $res = Invoke-Api POST /bets @{ roundId = $round.roundId; userId = $p.userId; multiplier = $p.multiplier; amount = $p.amount }
    Check ($res.Status -eq 201) ("{0} bets {1} at {2}x -> {3}" -f $p.userId, $p.amount, (Format-Money (To-Cents $p.multiplier)), $res.Status)
    if ($res.Status -eq 201) { $accepted += $p }
}

$dup = Invoke-Api POST /bets @{ roundId = $round.roundId; userId = 'user-1'; multiplier = 2; amount = 1 }
Check ($dup.Status -eq 409) "a second bet from user-1 is rejected -> $($dup.Status) $($dup.Body)"

Step '4. Count bets in the round'
$count = Invoke-Api GET "/rounds/$($round.roundId)/bets/count"
Check ($count.Status -eq 200 -and [int64]$count.Body -eq $accepted.Count) "GET /rounds/$($round.roundId)/bets/count -> $($count.Body) (expected $($accepted.Count))"

Step '5. Wait for the landing'
$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
$result = $null
while ($null -eq $result -and (Get-Date) -lt $deadline) {
    $history = (Invoke-Api GET /rounds/history).Body | ConvertFrom-Json
    $result = @($history) | Where-Object { $_.roundId -eq $round.roundId } | Select-Object -First 1
    if ($null -eq $result) { Start-Sleep -Milliseconds 500 }
}
Check ($null -ne $result) "round $($round.roundId) appears in GET /rounds/history"
if ($null -eq $result) { exit 1 }
$landing = To-Cents $result.multiplier
Write-Host ("  landed at {0}x" -f (Format-Money $landing))

Step '6. Wait for the payout'
$settled = $false
while (-not $settled -and (Get-Date) -lt $deadline) {
    $settled = (Invoke-Api GET "/rounds/$($round.roundId)/bets/count").Status -eq 404
    if (-not $settled) { Start-Sleep -Milliseconds 200 }
}
Check $settled 'round removed from the store after the payout'

Step '7. Check balances'
$rows = foreach ($p in $accepted) {
    $target = To-Cents $p.multiplier
    $won = $target -le $landing
    $prize = if ($won) { [int64]$p.amount * $target } else { 0 }
    $expected = $before[$p.userId] - [int64]$p.amount * 100 + $prize
    $actual = To-Cents ((Invoke-Api GET "/players/$($p.userId)/balance").Body | ConvertFrom-Json).balance
    Check ($actual -eq $expected) ("{0}: {1}, balance {2} (expected {3})" -f $p.userId, $(if ($won) { 'won' } else { 'lost' }), (Format-Money $actual), (Format-Money $expected))
    [pscustomobject]@{
        User       = $p.userId
        Amount     = $p.amount
        Target     = '{0}x' -f (Format-Money $target)
        Result     = if ($won) { 'won' } else { 'lost' }
        Before     = Format-Money $before[$p.userId]
        After      = Format-Money $actual
    }
}
$rows | Format-Table -AutoSize | Out-String | Write-Host

if ($failures -gt 0) {
    Write-Host "$failures check(s) failed" -ForegroundColor Red
    exit 1
}
Write-Host 'All checks passed' -ForegroundColor Green
exit 0
