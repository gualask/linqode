#!/bin/sh
# Emits JSONL on stdout, the shape Linqode's structured mode is built for:
# a level, a message, a timestamp, and nested fields that flatten to dotted
# paths (`http.status`). Levels rotate so filters and stats have something
# to separate.
set -eu

i=0
while true; do
	i=$((i + 1))
	case $((i % 6)) in
	0) level=error; status=500; msg="upstream timeout" ;;
	1 | 2 | 3) level=info; status=200; msg="request completed" ;;
	4) level=warn; status=429; msg="rate limited" ;;
	5) level=debug; status=200; msg="cache hit" ;;
	esac

	printf '{"ts":"%s","level":"%s","msg":"%s","seq":%d,"http":{"status":%d,"method":"GET","path":"/api/items"},"service":"api"}\n' \
		"$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$level" "$msg" "$i" "$status"
	sleep 1
done
