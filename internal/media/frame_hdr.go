// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
)

// ProbeFrameHDR reads the HDR side data of a video's first frame. HDR10 mastering data and content light level are
// often carried per frame, not on the stream, so the stream-level probe does not see them.
func ProbeFrameHDR(ctx context.Context, path string) (HDR, error) {
	arg, err := filepath.Abs(path)
	if err != nil {
		arg = path
	}
	cmd := exec.CommandContext(ctx, tool("ffprobe"), "-v", "error", "-select_streams", "v:0",
		"-read_intervals", "%+#1", "-show_frames", "-print_format", "json", arg)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return HDR{}, fmt.Errorf("read the first frame of %s: %w: %s", path, err, stderr.String())
	}
	var doc struct {
		Frames []struct {
			SideDataList []struct {
				Type string `json:"side_data_type"`
			} `json:"side_data_list"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		return HDR{}, fmt.Errorf("read the first frame of %s: %w", path, err)
	}
	var h HDR
	if len(doc.Frames) == 0 {
		return h, nil
	}
	for _, sd := range doc.Frames[0].SideDataList {
		switch sd.Type {
		case "Mastering display metadata":
			h.MasteringDisplay = true
		case "Content light level metadata":
			h.ContentLight = true
		}
	}
	return h, nil
}
