package localchan

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"strconv"
)

// LinuxAccess is the access of the socket on Linux: mode 0660, owner
// `mestre`, group `mestre-agente` (docs/arquitetura/canal-local.md#endpoint).
// An empty User or Group leaves that one as it is: the engine runs as
// `mestre` already, and in tests nothing is changed but the mode.
type LinuxAccess struct {
	Mode  fs.FileMode
	User  string
	Group string
}

// DefaultAccess is the access the installer sets up.
func DefaultAccess() Access {
	return LinuxAccess{Mode: 0o660, Group: "mestre-agente"}
}

// Apply sets the mode and the owner.
func (a LinuxAccess) Apply(path string) error {
	uid, gid := -1, -1
	if a.User != "" {
		u, err := user.Lookup(a.User)
		if err != nil {
			return fmt.Errorf("user %s: %w", a.User, err)
		}
		if uid, err = strconv.Atoi(u.Uid); err != nil {
			return fmt.Errorf("uid of %s: %w", a.User, err)
		}
	}
	if a.Group != "" {
		g, err := user.LookupGroup(a.Group)
		if err != nil {
			return fmt.Errorf("group %s: %w", a.Group, err)
		}
		if gid, err = strconv.Atoi(g.Gid); err != nil {
			return fmt.Errorf("gid of %s: %w", a.Group, err)
		}
	}
	if uid >= 0 || gid >= 0 {
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("chown: %w", err)
		}
	}
	if err := os.Chmod(path, a.Mode); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	return nil
}
