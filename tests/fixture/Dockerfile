# The fixture server: sshd in front of a real Docker daemon, which is what a
# host Linqode manages actually looks like. Docker-in-Docker means the demo
# project's containers are real containers, so `docker compose` behaves as it
# would in production rather than being stubbed.

FROM docker:29-dind

RUN apk add --no-cache openssh-server

# An ordinary account in the docker group — the same setup an operator would
# have on a real host. Linqode never needs root.
RUN addgroup -S docker 2>/dev/null || true; \
	adduser -D -s /bin/sh linqode && \
	adduser linqode docker && \
	passwd -u linqode 2>/dev/null || true

# No passwords: the fixture accepts exactly the key the test generates.
# Pinning HostKey to the mounted ed25519 key keeps the server identity stable
# across recreations — without it sshd could negotiate one of the other types
# ssh-keygen -A generates fresh in each container.
RUN mkdir -p /etc/ssh/sshd_config.d && \
	printf 'PasswordAuthentication no\nPermitRootLogin no\nPubkeyAuthentication yes\nHostKey /etc/ssh/ssh_host_ed25519_key\n' \
	> /etc/ssh/sshd_config.d/10-fixture.conf

COPY project /srv/demo
COPY entrypoint.sh /usr/local/bin/fixture-entrypoint.sh
RUN chmod +x /usr/local/bin/fixture-entrypoint.sh

EXPOSE 22

ENTRYPOINT ["/usr/local/bin/fixture-entrypoint.sh"]
