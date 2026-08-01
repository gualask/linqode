#!/bin/sh
# Brings up sshd alongside dockerd, then starts the demo project once the
# daemon is listening. dockerd stays PID 1 so the container's lifecycle is
# still the daemon's.
set -eu

# The host key is generated once on the host side and mounted in, so a
# recreated fixture keeps its identity: a client that already trusted it does
# not see a changed key. ssh-keygen -A fills in any other type without
# touching what is already there.
if [ -f /fixture/ssh_host_ed25519_key ]; then
	cp /fixture/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key
	chmod 600 /etc/ssh/ssh_host_ed25519_key
	ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key >/etc/ssh/ssh_host_ed25519_key.pub
fi
ssh-keygen -A

# The authorized key is mounted read-only with the host's ownership; copy it
# into place so it passes sshd's StrictModes checks.
mkdir -p /home/linqode/.ssh
if [ -f /fixture/authorized_keys ]; then
	cp /fixture/authorized_keys /home/linqode/.ssh/authorized_keys
fi
chown -R linqode:linqode /home/linqode/.ssh
chmod 700 /home/linqode/.ssh
chmod 600 /home/linqode/.ssh/authorized_keys 2>/dev/null || true

mkdir -p /var/empty
/usr/sbin/sshd -e

# The demo project can only start once dockerd is up, and dockerd is not up
# until we exec it below — so wait in the background.
(
	while ! docker info >/dev/null 2>&1; do
		sleep 1
	done
	cd /srv/demo && docker compose up -d
	# Marker the test polls on, so it never races the project's startup.
	touch /run/fixture-ready
) &

# dockerd-entrypoint.sh is the upstream dind entrypoint; args starting with a
# dash are passed through to dockerd (see docker-compose.yml).
exec dockerd-entrypoint.sh "$@"
