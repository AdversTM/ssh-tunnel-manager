# Generates test client keys into test/keys (OpenSSH + PPK, with and without passphrase)
# and restricts private key ACLs so Windows OpenSSH accepts them.
$ErrorActionPreference = 'Stop'
$keys = Join-Path $PSScriptRoot '..\keys'
New-Item -ItemType Directory -Force -Path $keys | Out-Null
$keys = (Resolve-Path $keys).Path

$script = @'
set -e
apk add --no-cache openssh-keygen putty >/dev/null
cd /out
rm -f client_test*
ssh-keygen -q -t ed25519 -N '' -C stm-test -f client_test
ssh-keygen -q -t ed25519 -N keypass -C stm-test-pp -f client_test_pp
puttygen client_test -O private -o client_test.ppk
echo keypass > /tmp/pp
puttygen client_test_pp --old-passphrase /tmp/pp --new-passphrase /tmp/pp -O private -o client_test_pp.ppk
'@ -replace "`r", ''

docker run --rm -v "${keys}:/out" alpine:3.20 sh -c $script
if ($LASTEXITCODE -ne 0) { throw "key generation failed: $LASTEXITCODE" }

foreach ($f in 'client_test', 'client_test_pp', 'client_test.ppk', 'client_test_pp.ppk') {
    $p = Join-Path $keys $f
    icacls $p /inheritance:r /grant:r "${env:USERNAME}:F" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "icacls failed for $p" }
}
Get-ChildItem $keys -Filter 'client_test*' | Select-Object Name, Length
