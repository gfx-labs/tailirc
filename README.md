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

## Clients

Targets are `ADDR#channel`. Channels are created on first use.

```sh
tailirc send 'tcXXXX#secrets' 'the password is hunter2'
printf '%s\n' "$TOKEN" | tailirc send 'tcXXXX#secrets'
tailirc read 'tcXXXX#secrets'          # whole buffer
tailirc read -n 5 'tcXXXX#secrets'
```

`send` and `read` are IRC clients: `read` uses IRCv3 `CHATHISTORY LATEST`.

### Aliases

Name an address once and use `ALIAS#channel` anywhere `ADDR#channel` works:

```sh
tailirc alias add team tcXXXX            # saved in ~/.config/tailcat/irc-aliases
tailirc alias add -e tmp tcYYYY          # ephemeral: $XDG_RUNTIME_DIR, gone on logout/reboot
tailirc read team#secrets
tailirc alias ls
tailirc alias rm tmp
```

Ephemeral aliases take precedence over saved ones with the same name. Alias files are written 0600 because an address contains the server's pre-shared key.

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
- `pkg/aliases`: saved and ephemeral address aliases
- `pkg/tckeys`: tailcat key loading, allow lists, nicks

`scripts/e2e.sh <binary>` runs a live check over real tailcat with throwaway keys.
