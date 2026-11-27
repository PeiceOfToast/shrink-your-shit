// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import "testing"

// The ENCODER tag a muxer writes into a video stream is read whatever its case: MKV writes it in upper case.
func TestVideoTrackReadsTheEncoderTag(t *testing.T) {
	data := []byte(`{"format":{"format_name":"matroska,webm","duration":"0.1"},"streams":[
		{"index":0,"codec_type":"video","codec_name":"hevc","width":256,"height":256,
		 "tags":{"ENCODER":"hevc_qsv"}}]}`)
	info, err := ParseProbe(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Video) != 1 || info.Video[0].Encoder != "hevc_qsv" {
		t.Fatalf("video = %+v; want the encoder tag hevc_qsv", info.Video)
	}
}

// A stream with no encoder tag reads as "".
func TestVideoTrackWithoutEncoderTagIsEmpty(t *testing.T) {
	data := []byte(`{"format":{"format_name":"mov","duration":"1"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264"}]}`)
	info, err := ParseProbe(data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Video[0].Encoder != "" {
		t.Fatalf("encoder = %q; want empty", info.Video[0].Encoder)
	}
}
