# Failure simulator for the stm-sshd test container.
# Usage: sim.ps1 <action> [port]
#   kill-sessions     kill all sshd session processes (server-side drop)
#   restart           restart container
#   occupy-port <p>   bind 127.0.0.1:<p> inside container (remote port busy)
#   free-port <p>     release port occupied by occupy-port
#   rotate-hostkey    regenerate server host key (restart included)
#   net-off / net-on  disconnect / reconnect container network
#   listeners         show listening TCP sockets inside container
#   fingerprint       show current server host key fingerprint
param(
    [Parameter(Mandatory = $true)][string]$Action,
    [int]$Port
)
$ErrorActionPreference = 'Stop'
$c = 'stm-sshd'
$net = 'stm-test_default'

function Invoke-Checked([scriptblock]$Block) {
    & $Block
    if ($LASTEXITCODE -ne 0) { throw "command failed ($LASTEXITCODE): $Block" }
}

switch ($Action) {
    'kill-sessions' {
        Invoke-Checked { docker exec $c sh -c 'for p in $(pgrep sshd); do [ "$p" != 1 ] && kill -9 "$p"; done; true' }
    }
    'restart' { Invoke-Checked { docker restart $c } }
    'occupy-port' {
        if (-not $Port) { throw 'port required' }
        Invoke-Checked { docker exec -d $c socat "TCP-LISTEN:$Port,bind=127.0.0.1,fork,reuseaddr" 'SYSTEM:echo busy' }
    }
    'free-port' {
        if (-not $Port) { throw 'port required' }
        # [T] keeps pkill from matching its own sh -c command line
        Invoke-Checked { docker exec $c sh -c "pkill -f '[T]CP-LISTEN:$Port,' ; true" }
    }
    'rotate-hostkey' {
        Invoke-Checked { docker exec $c rm -f /etc/ssh/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key.pub }
        Invoke-Checked { docker restart $c }
    }
    'net-off' { Invoke-Checked { docker network disconnect $net $c } }
    'net-on' { Invoke-Checked { docker network connect $net $c } }
    'listeners' { Invoke-Checked { docker exec $c netstat -ltnp } }
    'fingerprint' { Invoke-Checked { docker exec $c ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub } }
    default { throw "unknown action: $Action" }
}
