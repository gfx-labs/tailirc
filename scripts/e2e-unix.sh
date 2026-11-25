#!/usr/bin/env bash
# Live check of `connect --listen unix:PATH`, with throwaway keys.
set -u
bin="$1"
work=$(mktemp -d)
export XDG_CONFIG_HOME="$work/cfg"
A=$(tailcat genkey --client --key=client-default 2>&1 | grep -o 'nodekey:[0-9a-f]*' | head -1)
"$bin" serve --key=new --allow "$A" >"$work/srv.log" 2>&1 &
srv=$!
for _ in $(seq 30); do grep -q '^address' "$work/srv.log" && break; sleep 1; done
addr=$(awk '/^address/{print $2}' "$work/srv.log")
"$bin" send "$addr#general" "hello over tailcat"
sock="$work/irc.sock"
"$bin" connect --listen "unix:$sock" "$addr" 2>"$work/connect.log" &
con=$!
for _ in $(seq 30); do [ -S "$sock" ] && break; sleep 1; done
stat -c 'socket mode %a' "$sock"
printf 'CAP LS 302\r\nCAP REQ :batch draft/chathistory server-time\r\nCAP END\r\nNICK x\r\nUSER x 0 * :x\r\nCHATHISTORY LATEST #general * 10\r\nQUIT\r\n' |
	timeout 30 socat - "UNIX-CONNECT:$sock" | grep -E 'PRIVMSG|BATCH'
"$bin" connect --listen "unix:$sock" "$addr" 2>&1 | tail -1
kill "$con"; sleep 1
[ -e "$sock" ] && echo "socket left behind" || echo "socket removed on exit"
kill "$srv"
exit 0
