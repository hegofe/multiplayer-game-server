param(
    [int]$Count = 20
)

$root = Split-Path -Parent $PSScriptRoot
$cpu = (Get-CimInstance Win32_Processor | Select-Object -First 1).Name.Trim()
$ram = '{0} GB' -f [math]::Round((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1GB)
$os = (Get-CimInstance Win32_OperatingSystem).Caption

$script = @'
set +e
go test -race -count=$COUNT -run '^TestConcurrency' -json -coverprofile=/tmp/cover.out ./internal/game/ > /tmp/events.json
status=$?
go tool cover -func=/tmp/cover.out > /tmp/cover.txt
go run ./cmd/concurrency-report -data results/concurrency/data -events /tmp/events.json -coverage /tmp/cover.txt -out results/concurrency || status=1
exit $status
'@

docker run --rm `
    -v "${root}:/src" -w /src `
    -v aviator-gomod:/go/pkg/mod `
    -v aviator-gocache:/root/.cache/go-build `
    -e "COUNT=$Count" `
    -e "HOST_CPU=$cpu" `
    -e "HOST_RAM=$ram" `
    -e "HOST_OS=$os" `
    -e CONCURRENCY_REPORT_DIR=/src/results/concurrency/data `
    golang:1.27 sh -c ($script -replace "`r", '')
exit $LASTEXITCODE
