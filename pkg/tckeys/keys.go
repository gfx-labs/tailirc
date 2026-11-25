package tckeys

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// keyPath resolves a key name or path the same way the tailcat CLI does, so
// keys made with `tailcat genkey` are usable directly.
func keyPath(name string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return name, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tailcat", "keys", name+".private.json"), nil
}

// DefaultAllowFile is where serve reads allowed client keys when no
// --allow-file is given: $CONFIG/tailcat/allowed-ircs.
func DefaultAllowFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tailcat", "allowed-ircs"), nil
}

func LoadKey(name string) (*tailcat.PrivateKey, error) {
	path, err := keyPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var k tailcat.PrivateKey
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &k, nil
}

// LoadServerKey returns the saved server key, or nil for an ephemeral key:
// either name is "new", or name is "default" and no default key exists.
func LoadServerKey(name string) (*tailcat.PrivateKey, error) {
	if name == "new" {
		return nil, nil
	}
	k, err := LoadKey(name)
	if errors.Is(err, os.ErrNotExist) && name == "default" {
		return nil, nil
	}
	return k, err
}

func LoadClientKey(name string) (key.NodePrivate, error) {
	k, err := LoadKey(name)
	if errors.Is(err, os.ErrNotExist) {
		return key.NodePrivate{}, fmt.Errorf("no client key %q; create one with: tailcat genkey --client --key=%s", name, name)
	}
	if err != nil {
		return key.NodePrivate{}, err
	}
	return k.Private, nil
}

// AllowList is the set of client keys allowed to connect, with optional nicks
// given as name@key.
type AllowList struct {
	Keys tailcat.KeySet
	// names maps a key's hex prefix (its ID) to a name. Names may repeat,
	// since the ID is always shown next to them.
	names map[string]string
}

// Name returns the configured name for the client with hex id, or "".
func (a *AllowList) Name(id string) string {
	if a == nil {
		return ""
	}
	return a.names[strings.ToLower(id)]
}

// Identity returns the nick and ID the server gives key k: the configured
// name, else the hex prefix, and the hex prefix as ID.
func (a *AllowList) Identity(k key.NodePublic) (nick, id string) {
	id = Nick(k)
	if n := a.Name(id); n != "" {
		return n, id
	}
	return id, id
}

// Display formats a peer for showing: "name!id". A local name for id takes
// precedence over the nick the server sent. With no name, only id is shown.
func (a *AllowList) Display(nick, id string) string {
	if n := a.Name(id); n != "" {
		nick = n
	}
	if id == "" {
		return nick
	}
	if nick == "" || strings.EqualFold(nick, id) {
		return id
	}
	return nick + "!" + id
}

// Parse adds entries separated by commas, spaces or newlines. Each entry is a
// nodekey:... public key or a tailcat address (whose server key is used),
// optionally prefixed with "name@" to set that client's nick. "name@hexprefix"
// (the 8 hex characters shown as an unnamed nick) only renames that nick for
// display and does not allow anyone to connect. # starts a comment. It returns
// the number of keys added.
func (a *AllowList) Parse(text string) (int, error) {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' }) {
			name, keyText, named := strings.Cut(field, "@")
			if !named {
				keyText = field
			}
			if named && isHexPrefix(keyText) {
				if err := a.setName(name, strings.ToLower(keyText)); err != nil {
					return count, err
				}
				continue
			}
			k, err := parseAllowKey(keyText)
			if err != nil {
				return count, err
			}
			if named {
				if err := a.setName(name, Nick(k)); err != nil {
					return count, err
				}
			}
			a.Keys.Add(k)
			count++
		}
	}
	return count, nil
}

func isHexPrefix(s string) bool {
	return len(s) == NickLength && strings.Trim(strings.ToLower(s), "0123456789abcdef") == ""
}

func (a *AllowList) setName(name, id string) error {
	if !validNick(name) {
		return fmt.Errorf("invalid name %q: use letters, digits, - or _, up to 32 characters", name)
	}
	if a.names == nil {
		a.names = map[string]string{}
	}
	if old, ok := a.names[id]; ok && old != name {
		return fmt.Errorf("%s has two names: %q and %q", id, old, name)
	}
	a.names[id] = name
	return nil
}

// HostNick is the name of the person running serve --tui. Its ID is the
// server key's hex prefix.
const HostNick = "host"

// validNick accepts names safe to use as IRC nicks.
func validNick(name string) bool {
	if name == "" || len(name) > 32 || name[0] == '-' || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// LoadAllowFile parses path into a. A missing file is not an error when
// optional is true. It returns the number of keys added.
func (a *AllowList) LoadAllowFile(path string, optional bool) (int, error) {
	data, err := os.ReadFile(path)
	if optional && errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := a.Parse(string(data))
	if err != nil {
		return n, fmt.Errorf("%s: %w", path, err)
	}
	return n, nil
}

func parseAllowKey(field string) (key.NodePublic, error) {
	var k key.NodePublic
	if strings.HasPrefix(field, "tc") {
		info, err := tailcat.ParseAddr(tailcat.Addr(field))
		if err != nil {
			return k, fmt.Errorf("invalid tailcat address %.12s...: %w", field, err)
		}
		return info.ServerPublic.NodePublic, nil
	}
	if err := k.UnmarshalText([]byte(field)); err != nil {
		return k, fmt.Errorf("invalid allowed key %.20q, want nodekey:<64 hex> or a tailcat address: %w", field, err)
	}
	return k, nil
}

// Nick derives a nick from the hex form of a node public key.
func Nick(k key.NodePublic) string {
	return strings.TrimPrefix(k.String(), "nodekey:")[:NickLength]
}

const NickLength = 8
