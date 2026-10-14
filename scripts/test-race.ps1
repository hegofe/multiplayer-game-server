param(
    [string]$Packages = './...',
    [Parameter(ValueFromRemainingArguments = $true)][string[]]$Extra
)

$root = Split-Path -Parent $PSScriptRoot
docker run --rm `
    -v "${root}:/src" -w /src `
    -v aviator-gomod:/go/pkg/mod `
    -v aviator-gocache:/root/.cache/go-build `
    golang:1.27 go test -race $Packages @Extra
exit $LASTEXITCODE
