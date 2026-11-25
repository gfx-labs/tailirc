# tailirc

An IRC server hosted over [tailcat](https://github.com/tailscale/tailcat). One person serves, allowed clients connect by their tailcat client key. Messages live only in the host's memory and are gone when it stops.

## Keys

Each participant creates a client key once and sends the printed `nodekey:...` to the host:

```sh
tailcat genkey --client --key=client-default
```

Nicks are the first 8 hex characters of the client's public key and cannot be changed.

## Host

```sh
tailirc serve --allow nodekey:aaa...,nodekey:bbb...
tailirc serve --allow-file allowed.txt     # one key per line, # comments
```

Uses the saved tailcat `default` key if present (stable address), else an ephemeral one. `--key=new` forces an ephemeral address. `--history` sets messages kept per channel (default 1000).

## Clients

Targets are `ADDR#channel`. Channels are created on first use.

```sh
tailirc send 'tcXXXX#secrets' 'the password is hunter2'
printf '%s\n' "$TOKEN" | tailirc send 'tcXXXX#secrets'
tailirc read 'tcXXXX#secrets'          # whole buffer
tailirc read -n 5 'tcXXXX#secrets'
```

`send` and `read` are IRC clients: `read` uses IRCv3 `CHATHISTORY LATEST`.

For a regular IRC client (weechat, irssi, ...):

```sh
tailirc connect tcXXXX                 # listens on 127.0.0.1:6667
```

The server supports `batch`, `draft/chathistory`, `echo-message`, `message-tags` and `server-time`. It does not replay history on JOIN; clients request it with `CHATHISTORY`.

## Layout

- `cmd/tailirc`: entry point
- `pkg/cli`: commands (urfave/cli v3)
- `pkg/ircd`: in-memory IRC server
- `pkg/ircclient`: minimal client for `send`/`read`
- `pkg/tckeys`: tailcat key loading, allow lists, nicks

`scripts/e2e.sh <binary>` runs a live check over real tailcat with throwaway keys.
