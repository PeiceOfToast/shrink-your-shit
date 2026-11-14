package web

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/helper"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// In the image the entrypoint starts the helper, so with the socket answering detectMode must say
// app even though the container env is set. Before the fix it said docker.
func TestDetectModeHelperWinsInDocker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the helper and its socket are Linux only")
	}
	t.Setenv("container", "docker")
	dir := t.TempDir()
	saved := helper.HelperSocket
	defer func() { helper.HelperSocket = saved }()
	sock := filepath.Join(dir, "up.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	helper.HelperSocket = sock
	if mode, _ := detectMode(); mode != "app" {
		t.Errorf("container set, helper answers: expected app, got %q", mode)
	}
}

// writeUIDMap points uidMapFile at a fake map; the test restores it when it ends.
func writeUIDMap(t *testing.T, content string) {
	t.Helper()
	saved := uidMapFile
	t.Cleanup(func() { uidMapFile = saved })
	p := filepath.Join(t.TempDir(), "uid_map")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	uidMapFile = p
}

// setupDockerNoHelper gives the docker-mode setup text: container env set, helper socket absent.
func setupDockerNoHelper(t *testing.T) string {
	t.Helper()
	t.Setenv("container", "docker")
	saved := helper.HelperSocket
	defer func() { helper.HelperSocket = saved }()
	helper.HelperSocket = filepath.Join(t.TempDir(), "absent.sock")
	sh := jobs.Share{Type: "smb", Address: `\\nas\media`, Name: "Media", Guest: true}
	setup, ok := setupForMode(nil, sh).(map[string]any)
	if !ok {
		t.Fatal("no setup text in docker mode")
	}
	text, _ := setup["text"].(string)
	return text
}

// A normal container (full uid map) gets the cap lines, then the volume text as the fallback.
func TestDockerSetupOffersCapLines(t *testing.T) {
	writeUIDMap(t, "         0          0 4294967295\n")
	text := setupDockerNoHelper(t)
	for _, want := range []string{
		"# To let the app mount this share itself, add these lines under services: shrink-your-shit: and restart:",
		"    cap_add:\n      - SYS_ADMIN\n      - DAC_READ_SEARCH\n",
		"    security_opt:\n      - \"apparmor:unconfined\"\n",
		"# Or mount it with a Docker volume instead:",
		"# Add under the top-level volumes: in your compose file:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docker setup text missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "# To let the app") > strings.Index(text, "# Add under the top-level volumes") {
		t.Errorf("cap lines must come before the volume text:\n%s", text)
	}
}

// In a user namespace (uid map is not the full range) the app can't mount, so the text says so
// and offers no cap lines.
func TestDockerSetupUserNamespaceNoCapLines(t *testing.T) {
	writeUIDMap(t, "         0     100000      65536\n")
	text := setupDockerNoHelper(t)
	if !strings.Contains(text, "# This container runs in a user namespace (for example a Proxmox LXC), so it can't mount shares itself. Use a Docker volume:") {
		t.Errorf("user namespace text missing:\n%s", text)
	}
	if strings.Contains(text, "cap_add") || strings.Contains(text, "SYS_ADMIN") {
		t.Errorf("user namespace must not offer cap lines:\n%s", text)
	}
	if !strings.Contains(text, "# Add under the top-level volumes: in your compose file:") {
		t.Errorf("volume text missing in user namespace:\n%s", text)
	}
}
