// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"strings"
	"testing"
)

// Only the graphics families the workers list are probed: no Media Foundation, no V4L2.
func TestHardwareEncodersAreOnlyTheListedFamilies(t *testing.T) {
	for _, e := range hardwareEncoders {
		if strings.HasSuffix(e, "_mf") || strings.HasSuffix(e, "_v4l2m2m") {
			t.Errorf("%s is probed, but only NVIDIA, Intel, AMD, VA-API and Apple are listed", e)
		}
	}
}
