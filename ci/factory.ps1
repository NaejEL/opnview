<#
.SYNOPSIS
    Run one software-factory cycle headlessly against an approved spec, or run
    the project checks on their own.
.DESCRIPTION
    Everything is built and tested inside the Debian development container. No
    Go toolchain is installed on the host, so Docker and its compose plugin are
    a hard requirement of both modes.
.EXAMPLE
    pwsh -File ci/factory.ps1 specs/SPEC-example.md
.EXAMPLE
    pwsh -File ci/factory.ps1 specs/SPEC-example.md -Yolo
.EXAMPLE
    pwsh -File ci/factory.ps1 -Checks
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string] $Spec,

    [switch] $Yolo,

    [switch] $Checks
)

$ErrorActionPreference = 'Stop'

# The canonical invocation of the four project commands. The same string in
# every shell, no quoting, no host PATH knowledge.
$checksCommand = 'docker compose run --rm checks'

# Both modes need Docker: the toolchain lives in the container and nowhere else.
function Test-DockerAvailable {
    $docker = Get-Command docker -ErrorAction SilentlyContinue
    if (-not $docker) {
        Write-Host "Error: the Docker CLI was not found in PATH." -ForegroundColor Red
        Write-Host "opnview is built and tested only inside its Debian development" -ForegroundColor Red
        Write-Host "container; no Go toolchain is installed on the host. Install Docker" -ForegroundColor Red
        Write-Host "Desktop (Windows, macOS) or the docker.io package (Linux), start the" -ForegroundColor Red
        Write-Host "engine, and run this script again." -ForegroundColor Red
        return $false
    }

    & docker compose version *> $null
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Error: the Docker Compose plugin is unavailable." -ForegroundColor Red
        Write-Host "'docker compose version' failed, so '$checksCommand' cannot run." -ForegroundColor Red
        Write-Host "Install the compose plugin (package docker-compose-plugin) or update" -ForegroundColor Red
        Write-Host "Docker Desktop, then run this script again." -ForegroundColor Red
        return $false
    }

    return $true
}

if ($Checks) {
    if ($Spec) {
        Write-Host "Error: -Checks takes no spec path (got: $Spec)." -ForegroundColor Red
        exit 2
    }
    if (-not (Test-DockerAvailable)) {
        exit 127
    }
    Write-Host "Checks: $checksCommand"
    $ErrorActionPreference = 'Continue'
    & docker compose run --rm checks
    exit $LASTEXITCODE
}

if (-not $Spec) {
    Write-Host "Error: the path to an approved spec is required." -ForegroundColor Red
    Write-Host "Usage: pwsh -File ci/factory.ps1 <approved-spec-path> [-Yolo]" -ForegroundColor Red
    Write-Host "       pwsh -File ci/factory.ps1 -Checks" -ForegroundColor Red
    exit 2
}

if (-not (Test-Path -LiteralPath $Spec -PathType Leaf)) {
    Write-Error "Error: spec '$Spec' not found."
}

if (-not (Select-String -LiteralPath $Spec -Pattern 'Status: APPROVED' -SimpleMatch -Quiet)) {
    Write-Host "Error: spec '$Spec' does not carry the line 'Status: APPROVED'." -ForegroundColor Red
    Write-Host "A CI cycle requires an already-approved spec. Open Claude Code and run" -ForegroundColor Red
    Write-Host "  /factory-run `"$Spec`"" -ForegroundColor Red
    Write-Host "to get it approved in an interactive session." -ForegroundColor Red
    exit 1
}

if (-not (Test-DockerAvailable)) {
    exit 127
}

$claude = Get-Command claude -ErrorAction SilentlyContinue
if (-not $claude) {
    Write-Host "Error: command 'claude' not found in PATH." -ForegroundColor Red
    exit 127
}

# The agents get exactly the two checks entry points and nothing wider: not
# 'docker *', not 'docker compose *'. Host 'go', 'gofmt' and 'sqlite3' are
# deliberately absent — the toolchain exists only inside the container.
# This string is kept identical in ci/factory.sh.
$allowedTools = 'Read,Glob,Grep,Write,Edit,Bash(git *),Bash(bash *),Bash(shellcheck *),Bash(docker compose run --rm checks),Bash(docker compose run --rm schema-checks)'

$logDir = 'factory-logs'
if (-not (Test-Path -LiteralPath $logDir -PathType Container)) {
    New-Item -ItemType Directory -Path $logDir | Out-Null
}
$stamp = Get-Date -Format yyyyMMdd-HHmmss
$logFile = Join-Path $logDir "factory-$stamp.json"

$claudeArgs = @(
    '-p', "/factory-run '$Spec'"
    '--output-format', 'json'
    '--max-turns', '100'
)

if ($Yolo) {
    $claudeArgs += '--dangerously-skip-permissions'
    $mode = 'YOLO (permissions bypassed)'
}
else {
    $claudeArgs += @('--permission-mode', 'acceptEdits', '--allowedTools', $allowedTools)
    $mode = 'allowlist + acceptEdits'
}

Write-Host "Spec : $Spec"
Write-Host "Log  : $logFile"
Write-Host "Mode : $mode"

$ErrorActionPreference = 'Continue'
& claude @claudeArgs | Tee-Object -FilePath $logFile
$status = $LASTEXITCODE

Write-Host "claude exited with code $status"
exit $status
