package run

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MachineFile holds the identity of the machine a corvex home belongs to,
	// minted on first use and never changed afterwards.
	MachineFile = "machine-id"

	// machineIDBytes: 64 bits of randomness. This is not a secret and not a
	// namespace anybody else writes into — it only has to not collide with the
	// handful of machines that could ever share a home directory.
	machineIDBytes = 8
)

// MachineID returns the stable identity of the machine this corvex home belongs
// to, creating it on first use.
//
// # Why a file instead of the hostname
//
// A record's pid is only meaningful on the machine that wrote it, so liveness has
// to know whether "here" is "there". The hostname looked like the obvious key and
// is the wrong one: on darwin os.Hostname() returns the mDNS name, which the
// network rewrites. Join a network that already has a `box.local` and the machine
// becomes `box-2.local`; a DHCP lease can hand over a different name entirely.
// Under a hostname-keyed guard that single event makes every live run on the
// machine read as `unknown` — the listing empties out because the Wi-Fi changed.
//
// A random id written once into $CORVEX_HOME survives renames, reboots and
// network changes, costs one 17-byte file, needs no platform-specific code
// (/etc/machine-id does not exist on darwin; IOPlatformUUID needs ioreg) and is
// injectable for tests. The hostname is still recorded — it is what a human reads
// in a listing — but it no longer decides anything.
//
// # Limit, stated
//
// If $CORVEX_HOME is shared between machines (an NFS home directory), they share
// this id and each will treat the other's pids as local. That setup already
// breaks the index itself, which is a per-machine file by construction, so it is
// out of scope rather than newly broken.
func MachineID(home string) (string, error) {
	if home == "" {
		return "", fmt.Errorf("machine id: empty home path")
	}
	if id := readMachineID(home); id != "" {
		return id, nil
	}
	if err := os.MkdirAll(home, homeDirPerm); err != nil {
		return "", fmt.Errorf("machine id dir %s: %w", home, err)
	}
	buf := make([]byte, machineIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("machine id: reading entropy: %w", err)
	}
	id := hex.EncodeToString(buf)

	// Write a temp file and hard-link it into place. Link fails when the name
	// already exists, so two processes racing on a fresh home converge on one id
	// instead of overwriting each other — and no reader can observe a
	// half-written file, because the name only ever appears complete.
	tmp, err := os.CreateTemp(home, ".machine-id-*")
	if err != nil {
		return "", fmt.Errorf("machine id temp file in %s: %w", home, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.WriteString(id + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("machine id write %s: %w", name, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("machine id chmod %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("machine id close %s: %w", name, err)
	}
	path := filepath.Join(home, MachineFile)
	if err := os.Link(name, path); err != nil {
		// Lost the race: whoever won is the answer.
		if existing := readMachineID(home); existing != "" {
			return existing, nil
		}
		// The name exists but holds nothing usable — a hand-edited file, or a
		// truncated write from some earlier version. Replace it: refusing to
		// identify the machine forever because of one bad file would quietly send
		// every liveness decision back to the hostname.
		if os.IsExist(err) {
			_ = os.Remove(path)
			if os.Link(name, path) == nil {
				return id, nil
			}
			if existing := readMachineID(home); existing != "" {
				return existing, nil
			}
		}
		return "", fmt.Errorf("machine id link %s: %w", path, err)
	}
	return id, nil
}

// readMachineID reads the machine identity without creating it: "" when the file
// is missing, empty or malformed.
//
// Readers use this rather than MachineID on purpose. A Resolver owns no state and
// must not mint this machine's identity as a side effect of a listing — if it
// did, a reader on a fresh machine would invent an id that the next `corvex run`
// then has to live with.
func readMachineID(home string) string {
	if home == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, MachineFile))
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if id == "" || len(id) > 64 {
		return ""
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return id
}

// normalizeHost makes the hostname comparison as stable as a hostname can be,
// for records written before machine ids existed: case does not identify a
// machine, a trailing dot is the same name, and `.local` is the mDNS suffix that
// appears and disappears with the network. The rest of a domain is deliberate
// configuration and stays part of the name.
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimRight(h, ".")
	return strings.TrimSuffix(h, ".local")
}
