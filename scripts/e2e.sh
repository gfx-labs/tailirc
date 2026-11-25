#!/usr/bin/env bash
# Live end-to-end check of tailirc over real tailcat, with keys in an isolated config dir.
set -u
work=$(mktemp -d)
cd "$work"
export XDG_CONFIG_HOME="$work/cfg"
bin="$1"
gen() { tailcat genkey --client --key="$1" 2>&1 | grep -o 'nodekey:[0-9a-f]*' | head -1; }
A=$(gen client-default); B=$(gen bob); gen eve >/dev/null
echo "A=$A B=$B"
mkdir -p cfg/tailcat
printf '# team keys\nalice@%s  # alice\n\n  # bob below\n%s\n' "$A" "$B" >cfg/tailcat/allowed-ircs
"$bin" serve --key=new >srv.log 2>&1 &
srv=$!
for _ in $(seq 30); do grep -q '^address' srv.log && break; sleep 1; done
cat srv.log
ADDR=$(awk '/^address/{print $2}' srv.log)
"$bin" send "$ADDR#secrets" "hello from A"; echo "send=$?"
printf 'line1\nline2\n' | "$bin" send --key=bob "$ADDR#secrets"; echo "send2=$?"
"$bin" send "$ADDR#other" "other chan"
"$bin" read --key=bob "$ADDR#secrets"; echo "read=$?"
"$bin" read -n 1 "$ADDR#secrets"
printf 'bob@%s\n' "${B:8:8}" >names
"$bin" read --names names "$ADDR#secrets"; echo "names-read=$?"
"$bin" read "$ADDR#other"
timeout 45 "$bin" read --key=eve "$ADDR#secrets"; echo "eve=$?"
kill "$srv"
