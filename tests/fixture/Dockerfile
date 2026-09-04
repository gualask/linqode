# The fixture server: sshd in front of a real Docker daemon, which is what a
# host Linqode manages actually looks like. Docker-in-Docker means the demo
# project's containers are real containers, so `docker compose` behaves as it
# would in production rather than being stubbed.

# Pinned by digest, not by tag. `docker:29-dind` is a moving target: it
# followed the whole 29.x line, so a rebuild could silently swap the inner
# daemon underneath the fixture, re-download ~500 MB, and turn an unrelated
# upstream change into a failing test run. The digest is a multi-arch OCI
# index, so it resolves on amd64 and arm64 alike.
#
# To bump: `docker pull docker:29-dind` then
# `docker image inspect docker:29-dind --format '{{index .RepoDigests 0}}'`,
# and say in the commit which docker version it moved to.
#
# docker:29-dind — Docker 29.7.0, compose 5.3.1, buildx 0.36.0
FROM docker@sha256:084e385b0c9b7ab35d5a46dfedd033721448c000dbec71adcf13da8a9e71baa8

# The whole openssh group is named, not just the server, because the base
# image ships the client packages preinstalled at whatever revision it was
# built against. Once Alpine bumps openssh, `apk add openssh-server` pulls
# the new server, which requires the client packages at the matching
# revision, and apk refuses to upgrade the installed ones implicitly:
#
#   openssh-client-common-10.3_p1-r0:
#     breaks: openssh-client-default-10.3_p1-r1[openssh-client-common=10.3_p1-r1]
#
# Listing them together lets apk resolve the set to one revision. This is
# narrower than `apk upgrade --available`, which would move every package in
# the image and leave the fixture further from its base than it needs to be.
RUN apk add --no-cache \
	openssh-server openssh-client-common openssh-client-default openssh-keygen

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
