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
	for _, want := range []string{"sudo mount -t cifs", "/mnt/media", "pct set <id> -mp0 /mnt/media,mp=" + mountPointFor(sh), "pct restart <id>", "user namespace", owner} {
		if !strings.Contains(text, want) {
			t.Errorf("manual userns text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "uid=syp") || strings.Contains(text, "<host") {
		t.Errorf("the host mount must use the host's numbers:\n%s", text)
	}
	if rm, _ := setup["removeText"].(string); !strings.Contains(rm, "umount /mnt/media") || !strings.Contains(rm, "pct set") {
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
