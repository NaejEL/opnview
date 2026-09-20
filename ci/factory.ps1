<#
.SYNOPSIS
    Run one software-factory cycle headlessly against an approved spec.
.EXAMPLE
    pwsh -File ci/factory.ps1 specs/SPEC-example.md
.EXAMPLE
    pwsh -File ci/factory.ps1 specs/SPEC-example.md -Yolo
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string] $Spec,

    [switch] $Yolo
)

$ErrorActionPreference = 'Stop'

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

$claude = Get-Command claude -ErrorAction SilentlyContinue
if (-not $claude) {
    Write-Host "Error: command 'claude' not found in PATH." -ForegroundColor Red
    exit 127
}

$allowedTools = 'Read,Glob,Grep,Write,Edit,Bash(git *),Bash(go *),Bash(gofmt *),Bash(bash *),Bash(shellcheck *)'

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
