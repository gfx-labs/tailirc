# tailirc

An IRC server hosted over [tailcat](https://github.com/tailscale/tailcat). One person serves, allowed clients connect by their tailcat client key. Messages live only in the host's memory and are gone when it stops.

## Keys

Each participant creates a client key once and sends the printed `nodekey:...` to the host:

```sh
tailcat genkey --client --key=client-default
```

Nicks are the first 8 hex characters of the client's public key and cannot be changed.

## Host

Put allowed keys in `~/.config/tailcat/allowed-ircs`, one per line. `#` starts a comment, whole-line or trailing:

```
# team
nodekey:aaa...  # alice
nodekey:bbb...  # bob
```

```sh
tailirc serve                                  # reads ~/.config/tailcat/allowed-ircs
tailirc serve --allow-file other.txt           # replaces the default file
tailirc serve --allow nodekey:ccc...           # adds to the file's keys
```

Uses the saved tailcat `default` key if present (stable address), else an ephemeral one. `--key=new` forces an ephemeral address. `--history` sets messages kept per channel (default 1000).

`tailirc serve --tui` runs the server and puts a chat session in the foreground, so the host sees messages as they arrive. `--channel` picks the channel it opens (default `#general`).

## Clients

Targets are `ADDR#channel`. Channels are created on first use.

```sh
tailirc tui 'tcXXXX#secrets'           # interactive session, replays the channel's history
```

In the TUI, type to send, `/join #other` to switch channels, `/quit` to exit, PgUp/PgDn to scroll.

One-shot commands:

```sh
tailirc send 'tcXXXX#secrets' 'the password is hunter2'
printf '%s\n' "$TOKEN" | tailirc send 'tcXXXX#secrets'
tailirc read 'tcXXXX#secrets'          # whole buffer
tailirc read -n 5 'tcXXXX#secrets'
```

All clients use IRCv3 `CHATHISTORY` to read the buffer.

For a regular IRC client (weechat, irssi, ...):

```sh
tailirc connect tcXXXX                 # listens on 127.0.0.1:6667
```

The server supports `batch`, `draft/chathistory`, `echo-message`, `message-tags` and `server-time`. It does not replay history on JOIN; clients request it with `CHATHISTORY`.

## Layout

- `cmd/tailirc`: entry point
- `pkg/cli`: commands (urfave/cli v3)
- `pkg/ircd`: in-memory IRC server
- `pkg/ircclient`: minimal client for `send`, `read` and the TUI
- `pkg/tui`: tview chat session
- `pkg/tckeys`: tailcat key loading, allow lists, nicks

`scripts/e2e.sh <binary>` and `scripts/e2e-tui.sh <binary>` run live checks over real tailcat with throwaway keys.
