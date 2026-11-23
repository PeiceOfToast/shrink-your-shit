package web

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/helper"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// In an unprivileged container the manual setup must not offer a sudo mount to run here. It gives the
// host mount, the pct set line and the restart, and the option string is the one the plain case uses.
func TestManualSetupInAUserNamespaceMountsOnTheHost(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the user-namespace check is Linux only")
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("this machine is a container, so manual mode is not reachable here")
	}
	writeUIDMap(t, "         0     100000      65536\n")
	writeGIDMap(t, "         0     100000      65536\n")
	t.Setenv("container", "")
	saved := helper.HelperSocket
	defer func() { helper.HelperSocket = saved }()
	helper.HelperSocket = filepath.Join(t.TempDir(), "absent.sock")
	sh := jobs.Share{Type: "smb", Address: `\\nas\media`, Name: "Media", Guest: true}
	setup, ok := setupForMode(nil, sh).(map[string]any)
	if !ok {
		t.Fatal("no setup text in manual mode")
	}
	text, _ := setup["text"].(string)
	if setup["kind"] != "command" {
		t.Errorf("kind: %v", setup["kind"])
	}
	owner := fmt.Sprintf("uid=%d,gid=%d", 100000+os.Getuid(), 100000+os.Getgid())
	for _, want := range []string{"mkdir -p /mnt/media && printf '%s\\n' '//nas/media /mnt/media cifs guest,", owner + ",file_mode=0660,dir_mode=0770,_netdev 0 0' >> /etc/fstab && mount /mnt/media",
		"Use mp1, mp2", "pct set <id> -mp0 /mnt/media,mp=" + mountPointFor(sh), "pct restart <id>", "user namespace"} {
		if !strings.Contains(text, want) {
			t.Errorf("manual userns text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "sudo") { // a Proxmox host has no sudo by default; the steps run as root
		t.Errorf("host steps must not use sudo:\n%s", text)
	}
	if strings.Contains(text, "uid=syp") || strings.Contains(text, "<host") {
		t.Errorf("the host mount must use the host's numbers:\n%s", text)
	}
	if rm, _ := setup["removeText"].(string); !strings.Contains(rm, "umount /mnt/media") || !strings.Contains(rm, "/etc/fstab") || !strings.Contains(rm, "pct set") || strings.Contains(rm, "sudo") {
		t.Errorf("removeText: %q", rm)
	}
}

// A normal manual setup still mounts at the app's own folder, with the same options.
func TestManualSetupOutsideAUserNamespaceMountsHere(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the manual mode check is Linux only")
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("this machine is a container, so manual mode is not reachable here")
	}
	writeUIDMap(t, "         0          0 4294967295\n")
	t.Setenv("container", "")
	saved := helper.HelperSocket
	defer func() { helper.HelperSocket = saved }()
	helper.HelperSocket = filepath.Join(t.TempDir(), "absent.sock")
	sh := jobs.Share{Type: "smb", Address: `\\nas\media`, Name: "Media", Guest: true}
	setup, ok := setupForMode(nil, sh).(map[string]any)
	if !ok {
		t.Fatal("no setup text in manual mode")
	}
	text, _ := setup["text"].(string)
	if !strings.HasPrefix(text, "sudo mkdir -p ") || !strings.Contains(text, "sudo mount -t cifs") || strings.Contains(text, "pct set") {
		t.Errorf("manual text: %s", text)
	}
}

// writeGIDMap points gidMapFile at a fake map; the test restores it when it ends.
func writeGIDMap(t *testing.T, content string) {
	t.Helper()
	saved := gidMapFile
	t.Cleanup(func() { gidMapFile = saved })
	p := filepath.Join(t.TempDir(), "gid_map")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	gidMapFile = p
}

// With no readable map the host mount gets placeholders and a line saying how to work them out.
func TestManualUserNamespaceWithoutAMapGivesPlaceholders(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the user-namespace check is Linux only")
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("this machine is a container, so manual mode is not reachable here")
	}
	writeUIDMap(t, "")
	writeGIDMap(t, "")
	t.Setenv("container", "")
	saved := helper.HelperSocket
	defer func() { helper.HelperSocket = saved }()
	helper.HelperSocket = filepath.Join(t.TempDir(), "absent.sock")
	setup, ok := setupForMode(nil, jobs.Share{Type: "smb", Address: `\\nas\media`, Name: "Media", Guest: true}).(map[string]any)
	if !ok {
		t.Fatal("no setup text in manual mode")
	}
	text, _ := setup["text"].(string)
	if !strings.Contains(text, "uid=<host uid>,gid=<host gid>") || !strings.Contains(text, "Replace <host uid> and <host gid>") {
		t.Errorf("placeholders and the how-to line are missing:\n%s", text)
	}
}

// A share with a sign-in can't ask for its password at boot, so the fstab line reads a root-only credentials
// file, and the share address keeps a space as fstab's \040.
func TestManualUserNamespaceSignInUsesACredentialsFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the user-namespace check is Linux only")
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		t.Skip("this machine is a container, so manual mode is not reachable here")
	}
	writeUIDMap(t, "0 100000 65536\n")
	writeGIDMap(t, "0 100000 65536\n")
	t.Setenv("container", "")
	saved := helper.HelperSocket
	defer func() { helper.HelperSocket = saved }()
	helper.HelperSocket = filepath.Join(t.TempDir(), "absent.sock")
	sh := jobs.Share{Type: "smb", Address: `\\nas\my films`, Name: "Films", Username: `HOME\ann`}
	setup, _ := setupForMode(nil, sh).(map[string]any)
	text, _ := setup["text"].(string)
	for _, want := range []string{
		"(umask 077 && printf 'username=%s\\npassword=%s\\ndomain=%s\\n' 'ann' 'YOUR-PASSWORD' 'HOME' > /root/.syp-films.cred)",
		"'//nas/my\\040films /mnt/films cifs credentials=/root/.syp-films.cred,vers=3.1.1,",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "username=ann") || strings.Contains(text, "sudo") {
		t.Errorf("the fstab line must use the credentials file, without sudo:\n%s", text)
	}
	if rm, _ := setup["removeText"].(string); !strings.Contains(rm, "/root/.syp-films.cred") {
		t.Errorf("removeText must say to delete the credentials file: %q", rm)
	}
}
