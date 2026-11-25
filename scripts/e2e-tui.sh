#!/usr/bin/env bash
# Live check of `serve --tui` and `tui` in tmux, with throwaway keys.
set -u
bin="$1"
work=$(mktemp -d)
export XDG_CONFIG_HOME="$work/cfg"
gen() { tailcat genkey --client --key="$1" 2>&1 | grep -o 'nodekey:[0-9a-f]*' | head -1; }
A=$(gen client-default)
addr="$(tailcat genkey --key=srv 2>/dev/null | grep -o 'tc[A-Za-z0-9_-]*' | head -1)#general"
mkdir -p "$work/cfg/tailcat"
echo "$A # client" >"$work/cfg/tailcat/allowed-ircs"
s=tailirc-tui-$$
host=$(tmux new-session -d -P -F '#{pane_id}' -s "$s" -x 140 -y 20 -e XDG_CONFIG_HOME="$XDG_CONFIG_HOME" "$bin serve --tui --key=srv")
for _ in $(seq 30); do tmux capture-pane -pt "$host" | grep -q 'share:' && break; sleep 1; done
"$bin" send "$addr" "before client"
client=$(tmux split-window -P -F '#{pane_id}' -t "$s" -v -e XDG_CONFIG_HOME="$XDG_CONFIG_HOME" "$bin tui '$addr'")
for _ in $(seq 30); do tmux capture-pane -pt "$client" | grep -q 'before client' && break; sleep 1; done
tmux send-keys -t "$client" "hi from client" Enter
sleep 1
tmux send-keys -t "$host" "hi from host" Enter
sleep 2
echo "== host"; tmux capture-pane -pt "$host" | grep -v '^$'
echo "== client"; tmux capture-pane -pt "$client" | grep -v '^$'
tmux send-keys -t "$client" "/quit" Enter
tmux send-keys -t "$host" "/quit" Enter
sleep 1
tmux kill-session -t "$s" 2>/dev/null
exit 0
