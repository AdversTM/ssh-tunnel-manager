#!/bin/sh
set -e

if [ ! -f /etc/ssh/ssh_host_ed25519_key ]; then
  ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key
fi

cat /keys/*.pub > /home/keyuser/.ssh/authorized_keys
chown keyuser:keyuser /home/keyuser/.ssh/authorized_keys
chmod 600 /home/keyuser/.ssh/authorized_keys

exec /usr/sbin/sshd -D -e
