package web

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// stubDevices sets the device seams for one test and restores them when it ends.
func stubDevices(t *testing.T, dri bool, renders []string, nvidia bool) {
	t.Helper()
	origDRI, origNodes, origNvidia := usesDevDRI, renderNodes, nvidiaDevicePresent
	t.Cleanup(func() { usesDevDRI, renderNodes, nvidiaDevicePresent = origDRI, origNodes, origNvidia })
	usesDevDRI = dri
	renderNodes = func() []string { return renders }
	nvidiaDevicePresent = func() bool { return nvidia }
}

// deviceOf returns the device field of the capability for key, and whether it is set.
func deviceOf(t *testing.T, caps []map[string]any, key string) (string, bool) {
	t.Helper()
	for _, c := range caps {
		if c["key"] == key {
			d, ok := c["device"].(string)
			return d, ok
		}
	}
	t.Fatalf("no capability %q in %v", key, caps)
	return "", false
}

// Intel, VA-API and AMD encoders report the render node: found with one, missing without.
func TestEncoderDeviceRenderNodeFoundAndMissing(t *testing.T) {
	encs := []string{"hevc_qsv", "hevc_vaapi", "hevc_amf"}
	tools := doctor.Tools{Hardware: encs[:2], HardwareListed: encs[2:]}

	stubDevices(t, true, []string{"/dev/dri/renderD128"}, false)
	caps := capabilities(tools, true)
	for _, e := range encs {
		if d, ok := deviceOf(t, caps, "encoder:"+e); !ok || d != "found" {
			t.Errorf("%s with a render node: device = %q (set %v), want found", e, d, ok)
		}
	}

	stubDevices(t, true, nil, false)
	caps = capabilities(tools, true)
	for _, e := range encs {
		if d, ok := deviceOf(t, caps, "encoder:"+e); !ok || d != "missing" {
			t.Errorf("%s without a render node: device = %q (set %v), want missing", e, d, ok)
		}
	}
}

// NVENC reports /dev/nvidia0: found when it exists, missing when it does not, and the render
// nodes don't decide it.
func TestEncoderDeviceNVENCFoundAndMissing(t *testing.T) {
	tools := doctor.Tools{Hardware: []string{"hevc_nvenc"}}

	stubDevices(t, true, []string{"/dev/dri/renderD128"}, true)
	if d, _ := deviceOf(t, capabilities(tools, true), "encoder:hevc_nvenc"); d != "found" {
		t.Errorf("nvenc with /dev/nvidia0: device = %q, want found", d)
	}

	stubDevices(t, true, []string{"/dev/dri/renderD128"}, false)
	if d, _ := deviceOf(t, capabilities(tools, true), "encoder:hevc_nvenc"); d != "missing" {
		t.Errorf("nvenc without /dev/nvidia0: device = %q, want missing", d)
	}
}

// Software encoders never get a device field, and nothing gets one outside Linux.
func TestEncoderDeviceAbsentForSoftwareAndOffLinux(t *testing.T) {
	soft := doctor.Tools{Software: []string{"libx265"}, SoftwareListed: []string{"libsvtav1"}}
	stubDevices(t, true, []string{"/dev/dri/renderD128"}, true)
	for _, c := range capabilities(soft, true) {
		if _, has := c["device"]; has {
			t.Errorf("software encoder %v has a device field", c["key"])
		}
	}

	hw := doctor.Tools{Hardware: []string{"hevc_qsv", "hevc_nvenc"}, HardwareListed: []string{"hevc_vaapi"}}
	stubDevices(t, false, []string{"/dev/dri/renderD128"}, true)
	for _, c := range capabilities(hw, true) {
		if _, has := c["device"]; has {
			t.Errorf("off Linux %v has a device field", c["key"])
		}
	}
}
