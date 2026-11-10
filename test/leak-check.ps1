# AC-9: the test secret must not appear in ssh/plink command lines nor in app data,
# and tmp\ must be empty while tunnels are Up. Exit code 0 = clean, 1 = leak found.
# Pass the secret via STM_LEAK_SECRET so it does not end up in this script's own command line.
param(
    [string]$Secret = $env:STM_LEAK_SECRET,
    [string]$DataDir = (Join-Path $env:APPDATA 'SshTunnelManager')
)
$ErrorActionPreference = 'Stop'
if (-not $Secret) { throw 'Set STM_LEAK_SECRET (or -Secret) to the test password/passphrase.' }

$failures = @()

Get-CimInstance Win32_Process -Filter "Name='ssh.exe' OR Name='plink.exe'" | ForEach-Object {
    if ($_.CommandLine -and $_.CommandLine.Contains($Secret)) {
        $failures += "secret in command line of $($_.Name) pid $($_.ProcessId)"
    }
}

if (Test-Path -LiteralPath $DataDir) {
    Get-ChildItem -LiteralPath $DataDir -Recurse -File | Select-String -SimpleMatch -Pattern $Secret -List | ForEach-Object {
        $failures += "secret in file $($_.Path)"
    }
    $tmp = Join-Path $DataDir 'tmp'
    if (Test-Path -LiteralPath $tmp) {
        Get-ChildItem -LiteralPath $tmp -Force | ForEach-Object { $failures += "tmp not empty: $($_.FullName)" }
    }
} else {
    Write-Host "data dir $DataDir does not exist (nothing to scan)"
}

if ($failures.Count -gt 0) {
    $failures | ForEach-Object { Write-Host "LEAK: $_" }
    exit 1
}
Write-Host 'leak-check: clean'
exit 0
