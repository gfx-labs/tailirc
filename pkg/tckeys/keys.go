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

// ParseAllowKeys parses nodekey:... values separated by commas, spaces or
// newlines. Lines starting with # are comments.
func ParseAllowKeys(text string, set *tailcat.KeySet) (int, error) {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' }) {
			var k key.NodePublic
			if err := k.UnmarshalText([]byte(field)); err != nil {
				return count, fmt.Errorf("invalid allowed key %q: %w", field, err)
			}
			set.Add(k)
			count++
		}
	}
	return count, nil
}

// Nick derives a nick from the hex form of a node public key.
func Nick(k key.NodePublic) string {
	return strings.TrimPrefix(k.String(), "nodekey:")[:NickLength]
}

const NickLength = 8
