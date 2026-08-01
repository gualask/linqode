#!/usr/bin/env bash
# Local development driver: brings up the e2e fixture (sshd + docker-in-docker
# with the demo Compose project) and runs the TUI against it.
#
#   scripts/dev-fixture.sh          # docker engine -> fixture -> build -> TUI
#   scripts/dev-fixture.sh up       # fixture only, leave it running
#   scripts/dev-fixture.sh ssh      # shell on the fixture, as the operator would
#   scripts/dev-fixture.sh status   # what is running
#   scripts/dev-fixture.sh down     # tear the fixture down (add --colima to stop the VM)
#
# The fixture is left running after the TUI exits, so repeated runs are fast.
# Everything it needs — SSH keys, the image, the demo project — is created on
# first use and reused afterwards; see tests/fixture/README.md.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixture_dir="$repo_root/tests/fixture"
keys_dir="$fixture_dir/.keys"
identity="$keys_dir/id_ed25519"
known_hosts="$keys_dir/known_hosts"
binary="$repo_root/linqode"
config="$fixture_dir/linqode.toml"
host_name="fixture"

# Where sshd is published by tests/fixture/docker-compose.yml.
ssh_host="127.0.0.1"
ssh_port="2222"
ssh_user="linqode"

ready_timeout=300 # seconds; the first run builds the image and pulls busybox

# agent_pid is set only when this script starts its own ssh-agent, so the trap
# knows whether it owns one.
agent_pid=""

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
	if [ -n "$agent_pid" ]; then
		kill "$agent_pid" 2>/dev/null || true
	fi
}
trap cleanup EXIT

compose() { docker compose --project-directory "$fixture_dir" -f "$fixture_dir/docker-compose.yml" "$@"; }

# ensure_docker makes `docker info` answer, starting Colima if that is what is
# installed. Docker Desktop and a Linux engine are used as they are found.
ensure_docker() {
	command -v docker >/dev/null 2>&1 || die "docker CLI not found — see docs/docker-setup.md"

	if docker info >/dev/null 2>&1; then
		return
	fi

	if command -v colima >/dev/null 2>&1; then
		log "Docker daemon not reachable, starting Colima"
		colima start
	elif [ -d /Applications/Docker.app ]; then
		log "Docker daemon not reachable, starting Docker Desktop"
		open -a Docker
	else
		die "no Docker daemon and no way to start one — see docs/docker-setup.md"
	fi

	log "Waiting for the Docker daemon"
	for _ in $(seq 1 60); do
		docker info >/dev/null 2>&1 && return
		sleep 2
	done
	die "Docker daemon did not come up"
}

# ensure_keys generates the identity the fixture authorizes and the server's
# own host key, once. Keeping them means the agent entry and the ~/.ssh/known_hosts
# entry stay valid across fixture recreations; delete .keys/ to start over.
ensure_keys() {
	mkdir -p "$keys_dir"
	chmod 700 "$keys_dir"

	if [ ! -f "$identity" ]; then
		log "Generating the fixture identity"
		ssh-keygen -q -t ed25519 -N '' -C 'linqode fixture' -f "$identity"
	fi
	# The e2e TestMain writes authorized_keys but no .pub, so derive it from the
	# private key rather than assuming either file is there.
	if [ ! -f "$keys_dir/authorized_keys" ]; then
		ssh-keygen -y -f "$identity" >"$keys_dir/authorized_keys"
	fi

	if [ ! -f "$keys_dir/ssh_host_ed25519_key" ]; then
		log "Generating the fixture host key"
		ssh-keygen -q -t ed25519 -N '' -C 'linqode fixture host' -f "$keys_dir/ssh_host_ed25519_key"
	fi

	# A known_hosts scoped to the fixture, so this script's own ssh calls are
	# never affected by whatever else has used 127.0.0.1:2222 on this machine.
	printf '[%s]:%s %s\n' "$ssh_host" "$ssh_port" \
		"$(ssh-keygen -y -f "$keys_dir/ssh_host_ed25519_key")" >"$known_hosts"
}

# check_user_known_hosts guards the one thing this script cannot isolate: the
# TUI verifies host keys against ~/.ssh/known_hosts. A stale entry for
# 127.0.0.1:2222 — a previous fixture, or any other tunnel on that port — looks
# like a changed key and is refused, as it should be. Removing it is the user's
# call, so ask.
check_user_known_hosts() {
	local user_known_hosts="$HOME/.ssh/known_hosts"
	[ -f "$user_known_hosts" ] || return 0

	local existing expected
	existing=$(ssh-keygen -F "[$ssh_host]:$ssh_port" -f "$user_known_hosts" 2>/dev/null || true)
	[ -n "$existing" ] || return 0

	expected=$(ssh-keygen -y -f "$keys_dir/ssh_host_ed25519_key" | awk '{print $2}')
	case "$existing" in
	*"$expected"*) return 0 ;;
	esac

	warn "~/.ssh/known_hosts has a different key for [$ssh_host]:$ssh_port — the TUI will refuse to connect"
	if [ -t 0 ]; then
		read -r -p "Remove that entry and let the TUI trust the fixture on first use? [y/N] " reply
		case "$reply" in
		y | Y | yes)
			ssh-keygen -R "[$ssh_host]:$ssh_port" >/dev/null 2>&1
			log "Stale entry removed"
			;;
		*) warn "Left in place; the TUI will report a changed host key" ;;
		esac
	else
		warn "Remove it with: ssh-keygen -R '[$ssh_host]:$ssh_port'"
	fi
}

# ensure_agent puts the fixture identity where Linqode looks for it: the agent.
# Without SSH_AUTH_SOCK one is started for this script's lifetime only, so the
# key never lands in the user's long-lived agent.
ensure_agent() {
	if [ -z "${SSH_AUTH_SOCK:-}" ]; then
		log "No ssh-agent in the environment, starting one for this run"
		eval "$(ssh-agent -s)" >/dev/null
		agent_pid="$SSH_AGENT_PID"
	fi

	local fingerprint
	fingerprint=$(ssh-keygen -lf "$identity" | awk '{print $2}')
	if ssh-add -l 2>/dev/null | grep -qF "$fingerprint"; then
		return
	fi
	log "Adding the fixture identity to the agent"
	ssh-add "$identity" >/dev/null 2>&1 || warn "ssh-add failed; the TUI may not be able to authenticate"
}

fixture_running() {
	[ -n "$(compose ps -q server 2>/dev/null)" ]
}

# wait_ready blocks until the entrypoint's marker appears — sshd up, inner
# dockerd up, demo project started — and sshd actually answers.
wait_ready() {
	log "Waiting for the fixture (sshd, inner dockerd, demo project)"
	local deadline=$((SECONDS + ready_timeout))
	while [ $SECONDS -lt $deadline ]; do
		if compose exec -T server test -f /run/fixture-ready >/dev/null 2>&1 &&
			ssh_fixture true >/dev/null 2>&1; then
			log "Fixture ready on $ssh_user@$ssh_host:$ssh_port"
			return
		fi
		sleep 2
	done
	die "fixture not ready within ${ready_timeout}s — try: scripts/dev-fixture.sh status"
}

# ssh_fixture runs a command on the fixture with the generated identity and the
# fixture's own known_hosts, leaving the user's untouched.
ssh_fixture() {
	ssh -p "$ssh_port" -i "$identity" \
		-o IdentitiesOnly=yes \
		-o UserKnownHostsFile="$known_hosts" \
		-o ConnectTimeout=5 \
		"$ssh_user@$ssh_host" "$@"
}

cmd_up() {
	ensure_docker
	ensure_keys
	log "Building and starting the fixture"
	compose up -d --build
	wait_ready
}

cmd_run() {
	if fixture_running; then
		ensure_docker
		ensure_keys
		wait_ready
	else
		cmd_up
	fi
	ensure_agent
	check_user_known_hosts

	log "Building linqode"
	(cd "$repo_root" && go build -o "$binary" ./cmd/linqode)

	log "Starting the TUI against '$host_name' (q to quit)"
	"$binary" --config "$config" "$host_name" || true

	echo
	log "Fixture still running — 'scripts/dev-fixture.sh down' to stop it"
}

cmd_down() {
	log "Tearing the fixture down"
	compose down -v || true
	if [ "${1:-}" = "--colima" ]; then
		if command -v colima >/dev/null 2>&1; then
			log "Stopping Colima"
			colima stop
		else
			warn "colima not installed, nothing to stop"
		fi
	fi
}

cmd_status() {
	if command -v colima >/dev/null 2>&1; then
		log "Colima"
		colima status 2>&1 || true
	fi
	log "Fixture container"
	compose ps 2>&1 || true
	if fixture_running; then
		log "Demo project inside the fixture"
		ssh_fixture "docker compose -f /srv/demo/docker-compose.yml ps" 2>&1 || true
	fi
}

cmd_ssh() {
	fixture_running || die "fixture is not running — scripts/dev-fixture.sh up"
	ssh_fixture
}

case "${1:-run}" in
run) cmd_run ;;
up) cmd_up ;;
down) shift; cmd_down "$@" ;;
status) cmd_status ;;
ssh) cmd_ssh ;;
-h | --help | help)
	sed -n '2,13p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
	;;
*) die "unknown command '$1' — try: run | up | down | status | ssh" ;;
esac
