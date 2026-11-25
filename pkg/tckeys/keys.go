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
	Keys  tailcat.KeySet
	names map[key.NodePublic]string
	nicks map[string]key.NodePublic
	// prefixNames maps a key-derived nick (hex prefix) to a display name.
	prefixNames map[string]string
}

// Nick returns the configured name for k, or the key-derived nick.
func (a *AllowList) Nick(k key.NodePublic) string {
	if n, ok := a.names[k]; ok {
		return n
	}
	return a.Display(Nick(k))
}

// Display maps a nick seen on the wire to the local name for it: a
// name@hexprefix entry, or a name@key entry whose key has that prefix.
// Unknown nicks are returned unchanged.
func (a *AllowList) Display(nick string) string {
	if a == nil {
		return nick
	}
	if n, ok := a.prefixNames[strings.ToLower(nick)]; ok {
		return n
	}
	return nick
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
				if err := a.setPrefixName(name, strings.ToLower(keyText)); err != nil {
					return count, err
				}
				continue
			}
			k, err := parseAllowKey(keyText)
			if err != nil {
				return count, err
			}
			if named {
				if err := a.setName(name, k); err != nil {
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

func (a *AllowList) setPrefixName(name, prefix string) error {
	if !validNick(name) {
		return fmt.Errorf("invalid name %q: use letters, digits, - or _, up to 32 characters", name)
	}
	if a.prefixNames == nil {
		a.prefixNames = map[string]string{}
	}
	if old, ok := a.prefixNames[prefix]; ok && !strings.EqualFold(old, name) {
		return fmt.Errorf("%s has two names: %q and %q", prefix, old, name)
	}
	for p, n := range a.prefixNames {
		if p != prefix && strings.EqualFold(n, name) {
			return fmt.Errorf("name %q is given to both %s and %s", name, p, prefix)
		}
	}
	a.prefixNames[prefix] = name
	return nil
}

func (a *AllowList) setName(name string, k key.NodePublic) error {
	if !validNick(name) {
		return fmt.Errorf("invalid name %q: use letters, digits, - or _, up to 32 characters", name)
	}
	if a.names == nil {
		a.names = map[key.NodePublic]string{}
		a.nicks = map[string]key.NodePublic{}
	}
	folded := strings.ToLower(name)
	if other, ok := a.nicks[folded]; ok && other != k {
		return fmt.Errorf("name %q is given to two different keys", name)
	}
	if old, ok := a.names[k]; ok && !strings.EqualFold(old, name) {
		return fmt.Errorf("key %s has two names: %q and %q", Nick(k), old, name)
	}
	a.names[k] = name
	a.nicks[folded] = k
	// Clients only see the hex prefix of an unnamed peer, so a name@key entry
	// also renames that prefix when displayed.
	return a.setPrefixName(name, Nick(k))
}

// validNick accepts names safe to use as IRC nicks. A name that is 8 hex
// characters could collide with a key-derived nick, so it is rejected.
func validNick(name string) bool {
	if name == "" || len(name) > 32 || name[0] == '-' || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return !isHexPrefix(name)
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
