package web

import (
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
	writeUIDMap(t, "         0       1000          1\n")
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
	for _, want := range []string{"sudo mount -t cifs", "/mnt/media", "pct set <id> -mp0 /mnt/media,mp=/mnt/media", "pct restart <id>", "user namespace"} {
		if !strings.Contains(text, want) {
			t.Errorf("manual userns text missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "guest,vers=3.1.1,soft,nosuid,nodev,noexec,uid=syp,gid=syp,file_mode=0660,dir_mode=0770") {
		t.Errorf("mount options changed:\n%s", text)
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
