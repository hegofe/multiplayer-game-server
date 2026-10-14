param(
    [int]$Runs = 3,
    [int]$Port = 8080,
    [string[]]$Only
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')

$scenarios = @(
    [pscustomobject]@{ Name = 'base';       Bets = 5000;  Mode = 'uniform' },
    [pscustomobject]@{ Name = 'burst';      Bets = 5000;  Mode = 'burst' },
    [pscustomobject]@{ Name = 'stress-2x';  Bets = 10000; Mode = 'uniform' },
    [pscustomobject]@{ Name = 'stress-5x';  Bets = 25000; Mode = 'uniform' },
    [pscustomobject]@{ Name = 'stress-10x'; Bets = 50000; Mode = 'uniform' }
)
if ($Only) { $scenarios = $scenarios | Where-Object { $Only -contains $_.Name } }

$busy = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue
if ($busy) {
    Write-Host "Port $Port is in use. Stop the running server first: two servers sharing Redis would overwrite each other's active round." -ForegroundColor Red
    exit 1
}

Push-Location $root
$server = $null
try {
    docker compose up -d --wait *> $null
    if ($LASTEXITCODE -ne 0) { Write-Host "Redis did not start." -ForegroundColor Red; exit 1 }
    go build -o bin\server.exe .\cmd\server
    if ($LASTEXITCODE -ne 0) { exit 1 }

    $maxBets = ($scenarios | Measure-Object -Property Bets -Maximum).Maximum
    $env:USERS = "$maxBets"
    $env:PORT = "$Port"
    $serverLog = Join-Path $env:TEMP 'aviator-load-server.log'
    $server = Start-Process -FilePath .\bin\server.exe -RedirectStandardOutput $serverLog -RedirectStandardError "$serverLog.err" -PassThru -WindowStyle Hidden
    Remove-Item env:USERS, env:PORT

    $deadline = (Get-Date).AddSeconds(30)
    do {
        Start-Sleep -Milliseconds 500
        try { $ready = (Invoke-WebRequest -UseBasicParsing "http://localhost:$Port/health").StatusCode -eq 200 } catch { $ready = $false }
    } until ($ready -or (Get-Date) -gt $deadline)
    if (-not $ready) { Write-Host 'The server did not become healthy.' -ForegroundColor Red; exit 1 }
    Write-Host "Server ready on port $Port with $maxBets users (log: $serverLog)"

    foreach ($s in $scenarios) {
        $dir = Join-Path 'results\load\data' $s.Name
        New-Item -ItemType Directory -Force $dir | Out-Null
        for ($run = 1; $run -le $Runs; $run++) {
            $out = "results/load/data/$($s.Name)/run-$run.json"
            $line = k6 run --quiet -e "BASE_URL=http://localhost:$Port" -e "BETS=$($s.Bets)" -e "MODE=$($s.Mode)" -e "NAME=$($s.Name)" -e "OUT=$out" loadtest\bets.js 2>$null | Select-Object -Last 1
            Write-Host "[$run/$Runs] $line"
        }
    }

    $cpu = (Get-CimInstance Win32_Processor | Select-Object -First 1)
    $env:HOST_CPU = "$($cpu.Name.Trim()) ($($cpu.NumberOfLogicalProcessors) logical processors)"
    $env:HOST_RAM = '{0} GB' -f [math]::Round((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1GB)
    $env:HOST_OS = (Get-CimInstance Win32_OperatingSystem).Caption
    $env:K6_VERSION = ((k6 version) -split ' ')[1]
    go run ./cmd/load-report -data results/load/data -out results/load
} finally {
    if ($server -and -not $server.HasExited) { Stop-Process -Id $server.Id }
    Remove-Item env:HOST_CPU, env:HOST_RAM, env:HOST_OS, env:K6_VERSION -ErrorAction SilentlyContinue
    Pop-Location
}
