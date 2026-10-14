param(
    [switch]$Verify,
    [switch]$Down,
    [int]$Users = 10,
    [string]$TestBettingWindow = '60s',
    [int]$Port = 8080,
    [string]$Region = 'eu-west-1'
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')

$miniStackImage = 'ministackorg/ministack:1.5.17'
$miniStack = 'aviator-ministack'
$endpoint = 'http://host.docker.internal:4566'
$registry = 'host.docker.internal:4566'
$repository = 'aviator-server'
$cacheCluster = 'aviator-redis'
$cluster = 'aviator'
$k3s = "ministack-eks-$Region-$cluster"
$kubeconfig = Join-Path $env:TEMP 'aviator-eks.kubeconfig'
$verifyPort = 18080

function Step([string]$text) { Write-Host "`n== $text" -ForegroundColor Cyan }
function Fail([string]$text) { Write-Host $text -ForegroundColor Red; exit 1 }

function Aws {
    docker run --rm -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e "AWS_DEFAULT_REGION=$Region" `
        amazon/aws-cli --endpoint-url $endpoint @args 2>$null
}

function Kube { kubectl --kubeconfig $kubeconfig @args }

function Wait-Until([scriptblock]$condition, [int]$seconds, [string]$what) {
    $deadline = (Get-Date).AddSeconds($seconds)
    while (-not (& $condition)) {
        if ((Get-Date) -gt $deadline) { Fail "Timed out waiting for $what." }
        Start-Sleep -Seconds 2
    }
}

function Test-Healthy([int]$p) {
    try { (Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://localhost:$p/health").StatusCode -eq 200 } catch { $false }
}

function Get-ContainerState([string]$name) {
    $running = docker inspect -f '{{.State.Running}}' $name 2>$null
    if ($LASTEXITCODE -ne 0) { return 'missing' }
    if ($running -eq 'true') { return 'running' }
    return 'stopped'
}

function Start-PortForward([int]$p) {
    Start-Process kubectl -ArgumentList '--kubeconfig', $kubeconfig, '-n', 'aviator', 'port-forward', 'svc/aviator', "${p}:80" -PassThru -WindowStyle Hidden
}

function Assert-Tools {
    foreach ($tool in 'docker', 'kubectl') {
        if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { Fail "$tool is not installed or not on PATH. Install Docker Desktop." }
    }
    docker info *> $null
    if ($LASTEXITCODE -eq 0) { return }
    $desktop = Join-Path $env:ProgramFiles 'Docker\Docker\Docker Desktop.exe'
    if (-not (Test-Path $desktop)) { Fail 'Docker is not running and Docker Desktop was not found.' }
    Write-Host 'Starting Docker Desktop...'
    Start-Process $desktop
    Wait-Until { docker info *> $null; $LASTEXITCODE -eq 0 } 180 'Docker Desktop'
}

function Start-MiniStack {
    $state = Get-ContainerState $miniStack
    if ($state -eq 'running') { return }
    if ($state -eq 'stopped') {
        docker start $miniStack | Out-Null
    } else {
        if (Get-NetTCPConnection -State Listen -LocalPort 4566 -ErrorAction SilentlyContinue) {
            Fail 'Port 4566 is already in use, probably by another MiniStack or LocalStack. Stop it first.'
        }
        docker run -d --name $miniStack -p 4566:4566 -v /var/run/docker.sock:/var/run/docker.sock $miniStackImage | Out-Null
    }
    Wait-Until { try { (Invoke-WebRequest -UseBasicParsing 'http://localhost:4566/_ministack/health').StatusCode -eq 200 } catch { $false } } 60 'MiniStack'
}

function Wait-K3s {
    Wait-Until {
        if ((Get-ContainerState $k3s) -eq 'stopped') { docker start $k3s | Out-Null }
        (docker exec $k3s kubectl get nodes 2>$null) -match ' Ready '
    } 180 'the EKS node'
}

function Invoke-Deploy {
    Step "MiniStack, the local AWS emulator ($miniStackImage)"
    Start-MiniStack
    Write-Host '  ready on http://localhost:4566'

    Step 'ECR: build the image and push it'
    if (-not (Aws ecr describe-repositories --repository-names $repository)) {
        Aws ecr create-repository --repository-name $repository | Out-Null
    }
    docker build -q -t $repository $root | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail 'docker build failed.' }
    docker tag $repository "localhost:4566/${repository}:latest"
    docker push -q "localhost:4566/${repository}:latest" | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail 'docker push to ECR failed.' }
    Write-Host "  pushed $repository`:latest"

    Step 'ElastiCache: Redis'
    if (-not (Aws elasticache describe-cache-clusters --cache-cluster-id $cacheCluster)) {
        Aws elasticache create-cache-cluster --cache-cluster-id $cacheCluster --engine redis --cache-node-type cache.t3.micro --num-cache-nodes 1 | Out-Null
    }
    Wait-Until { (Aws elasticache describe-cache-clusters --cache-cluster-id $cacheCluster --query 'CacheClusters[0].CacheClusterStatus' --output text) -eq 'available' } 120 'ElastiCache'
    $redisPort = Aws elasticache describe-cache-clusters --cache-cluster-id $cacheCluster --show-cache-node-info --query 'CacheClusters[0].CacheNodes[0].Endpoint.Port' --output text
    $redisAddr = "host.docker.internal:$redisPort"
    Write-Host "  available at $redisAddr"

    Step 'EKS: Kubernetes cluster'
    if (-not (Aws eks describe-cluster --name $cluster)) {
        Aws eks create-cluster --name $cluster --role-arn arn:aws:iam::000000000000:role/eks --resources-vpc-config 'subnetIds=subnet-a,subnet-b' | Out-Null
    }
    Wait-Until { (Aws eks describe-cluster --name $cluster --query 'cluster.status' --output text) -eq 'ACTIVE' } 180 'the EKS cluster'
    Wait-K3s
    $apiServer = Aws eks describe-cluster --name $cluster --query 'cluster.endpoint' --output text
    (docker exec $k3s cat /etc/rancher/k3s/k3s.yaml) -replace 'https://127.0.0.1:6443', $apiServer | Set-Content -Encoding ascii $kubeconfig
    $hostsDir = "/var/lib/rancher/k3s/agent/etc/containerd/certs.d/$registry"
    docker exec $k3s mkdir -p $hostsDir
    "server = `"http://$registry`"`n`n[host.`"http://$registry`"]`n  capabilities = [`"pull`", `"resolve`"]`n" | docker exec -i $k3s tee "$hostsDir/hosts.toml" | Out-Null
    Write-Host "  ACTIVE at $apiServer"

    Step 'Kubernetes: deploy the service'
    Kube apply -f (Join-Path $root 'deploy\k8s\namespace.yaml') | Out-Null
    $configMap = Join-Path $env:TEMP 'aviator-config.yaml'
    Kube -n aviator create configmap aviator-config --from-literal=REDIS_ADDR=$redisAddr --from-literal=USERS=$Users --from-literal=TEST_BETTING_WINDOW=$TestBettingWindow --dry-run=client -o yaml | Set-Content -Encoding ascii $configMap
    Kube apply -f $configMap | Out-Null
    Kube apply -k (Join-Path $root 'deploy\k8s') | Out-Null
    Kube -n aviator rollout restart deployment/aviator | Out-Null
    Kube -n aviator rollout status deployment/aviator --timeout=180s
    if ($LASTEXITCODE -ne 0) { Fail 'The deployment did not become ready.' }
}

function Invoke-Serve {
    $forward = $null
    try {
        Write-Host "`nAviator is running on http://localhost:$Port" -ForegroundColor Green
        Write-Host "Server logs follow. Ctrl+C stops forwarding; the deployment keeps running. Delete everything with: .\scripts\start.ps1 -Down`n"
        while ($true) {
            if ($null -eq $forward -or $forward.HasExited) {
                $forward = Start-PortForward $Port
                Wait-Until { Test-Healthy $Port } 30 "the port-forward on port $Port"
            }
            Kube -n aviator logs -f deployment/aviator --tail=20
            Start-Sleep -Seconds 2
        }
    } finally {
        if ($forward -and -not $forward.HasExited) { Stop-Process -Id $forward.Id -ErrorAction SilentlyContinue }
    }
}

function Invoke-Verify {
    if (-not (Test-Path $kubeconfig)) { Fail 'Nothing deployed yet. Run: .\scripts\start.ps1' }
    $forward = Start-PortForward $verifyPort
    try {
        Wait-Until { Test-Healthy $verifyPort } 30 'the port-forward'
        & (Join-Path $PSScriptRoot 'manual-test.ps1') -BaseUrl "http://localhost:$verifyPort"
        $testExit = $LASTEXITCODE
    } finally {
        Stop-Process -Id $forward.Id -ErrorAction SilentlyContinue
    }

    Step 'Graceful shutdown: delete the pod and watch its logs'
    $pod = Kube -n aviator get pods -l app=aviator -o 'jsonpath={.items[0].metadata.name}'
    $logFile = Join-Path $env:TEMP 'aviator-pod-shutdown.log'
    $follow = Start-Process kubectl -ArgumentList '--kubeconfig', $kubeconfig, '-n', 'aviator', 'logs', '-f', $pod -RedirectStandardOutput $logFile -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 2
    Kube -n aviator delete pod $pod --wait=true | Out-Null
    Start-Sleep -Seconds 1
    Stop-Process -Id $follow.Id -ErrorAction SilentlyContinue
    Get-Content $logFile | Select-String 'shutdown|server stopped' | ForEach-Object { '  ' + $_.Line.Substring([math]::Max(0, $_.Line.IndexOf('msg='))) }
    Kube -n aviator rollout status deployment/aviator --timeout=120s | Out-Null
    $newPod = Kube -n aviator get pods -l app=aviator -o 'jsonpath={.items[0].metadata.name}'
    Write-Host "  Kubernetes replaced $pod with $newPod"
    exit $testExit
}

function Invoke-Down {
    Step 'Delete the emulated AWS resources'
    if ((Get-ContainerState $miniStack) -eq 'running') {
        Aws eks delete-cluster --name $cluster | Out-Null
        Aws elasticache delete-cache-cluster --cache-cluster-id $cacheCluster | Out-Null
        Aws ecr delete-repository --repository-name $repository --force | Out-Null
        Start-Sleep -Seconds 3
    }
    docker rm -f $k3s "ministack-elasticache-000000000000-$Region-$cacheCluster" $miniStack 2>$null | Out-Null
    docker rmi "localhost:4566/${repository}:latest" 2>$null | Out-Null
    Remove-Item -LiteralPath $kubeconfig -ErrorAction SilentlyContinue
    Write-Host '  MiniStack, the EKS cluster and the ElastiCache Redis are gone.'
}

if ($Down) { Invoke-Down; exit 0 }
if ($Verify) { Invoke-Verify }

Assert-Tools
if (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue) {
    Fail "Port $Port is already in use. Stop whatever is listening on it or pass -Port."
}
Invoke-Deploy
Invoke-Serve
