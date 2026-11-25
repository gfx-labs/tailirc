// Package aliases maps short names to tailcat addresses. Saved aliases live in
// $CONFIG/tailcat/irc-aliases. Ephemeral aliases live in
// $XDG_RUNTIME_DIR/tailirc/aliases, which is cleared on logout or reboot.
// Both files are "name address" lines with # comments, written 0600 because
// an address contains the server's pre-shared key.
package aliases

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Alias is one name to address mapping.
type Alias struct {
	Name      string
	Addr      string
	Ephemeral bool
}

// SavedPath returns the file holding saved aliases.
func SavedPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tailcat", "irc-aliases"), nil
}

// EphemeralPath returns the file holding ephemeral aliases.
func EphemeralPath() (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return "", errors.New("XDG_RUNTIME_DIR is not set, so ephemeral aliases are unavailable")
	}
	return filepath.Join(dir, "tailirc", "aliases"), nil
}

func path(ephemeral bool) (string, error) {
	if ephemeral {
		return EphemeralPath()
	}
	return SavedPath()
}

func read(ephemeral bool) ([]Alias, error) {
	p, err := path(ephemeral)
	if err != nil {
		if ephemeral {
			return nil, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Alias
	for n, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want \"name address\"", p, n+1)
		}
		list = append(list, Alias{Name: fields[0], Addr: fields[1], Ephemeral: ephemeral})
	}
	return list, nil
}

func write(ephemeral bool, list []Alias) error {
	p, err := path(ephemeral)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for _, a := range list {
		fmt.Fprintf(&b, "%s %s\n", a.Name, a.Addr)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// List returns ephemeral aliases followed by saved ones.
func List() ([]Alias, error) {
	eph, err := read(true)
	if err != nil {
		return nil, err
	}
	saved, err := read(false)
	if err != nil {
		return nil, err
	}
	return append(eph, saved...), nil
}

// Lookup returns the address for name. Ephemeral aliases take precedence.
func Lookup(name string) (string, bool, error) {
	list, err := List()
	if err != nil {
		return "", false, err
	}
	for _, a := range list {
		if a.Name == name {
			return a.Addr, true, nil
		}
	}
	return "", false, nil
}

// Set adds or replaces name. Comments in the file are not preserved.
func Set(name, addr string, ephemeral bool) error {
	if name == "" || strings.ContainsAny(name, "# \t\n") || strings.HasPrefix(name, "tc") {
		return fmt.Errorf("invalid alias name %q: no spaces or #, and must not start with \"tc\"", name)
	}
	list, err := read(ephemeral)
	if err != nil {
		return err
	}
	list = slices.DeleteFunc(list, func(a Alias) bool { return a.Name == name })
	list = append(list, Alias{Name: name, Addr: addr, Ephemeral: ephemeral})
	return write(ephemeral, list)
}

// Remove deletes name from both stores and reports whether it existed.
func Remove(name string) (bool, error) {
	removed := false
	for _, ephemeral := range []bool{true, false} {
		list, err := read(ephemeral)
		if err != nil {
			return removed, err
		}
		kept := slices.DeleteFunc(list, func(a Alias) bool { return a.Name == name })
		if len(kept) != len(list) {
			removed = true
			if err := write(ephemeral, kept); err != nil {
				return removed, err
			}
		}
	}
	return removed, nil
}
