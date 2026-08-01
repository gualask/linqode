#!/bin/sh
# Emits plain-text lines, so the same fixture covers the unstructured path:
# detection must NOT switch this service to structured rendering.
set -eu

i=0
while true; do
	i=$((i + 1))
	printf '%s INFO  web: request served path=/health seq=%d\n' \
		"$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$i"
	sleep 1
done
