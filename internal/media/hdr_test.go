// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"strings"
	"testing"
)

// S3: what the probe reads for HDR and Dolby Vision (RESEARCH/hdr-dv.md), from the stream and from
// the first frame.

const hdrStream = `{"streams":[{"codec_type":"video","codec_name":"hevc","color_transfer":"smpte2084","color_primaries":"bt2020","color_space":"bt2020nc",
 "side_data_list":[
  {"side_data_type":"Mastering display metadata","red_x":"34000/50000","red_y":"16000/50000","green_x":"13250/50000","green_y":"34500/50000",
   "blue_x":"7500/50000","blue_y":"3000/50000","white_point_x":"15635/50000","white_point_y":"16450/50000","min_luminance":"50/10000","max_luminance":"10000000/10000"},
  {"side_data_type":"Content light level metadata","max_content":1000,"max_average":400},
  {"side_data_type":"DOVI configuration record","dv_version_major":1,"dv_profile":8,"dv_level":6,"rpu_present_flag":1,"dv_bl_signal_compatibility_id":1}]}],
 "format":{"format_name":"matroska,webm","duration":"60"}}`

func TestHDRSideData(t *testing.T) {
	i, err := ParseProbe([]byte(hdrStream))
	if err != nil {
		t.Fatal(err)
	}
	v := i.Video()
	if v.MasteringDisplay != "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)" || v.CLL != "1000,400" ||
		!v.DolbyVision || v.DVProfile != 8 || v.DVCompat == nil || *v.DVCompat != 1 || !i.IsHDR() || i.Probe != ProbeVersion {
		t.Errorf("%+v", v)
	}
	// compatibility id 0 is kept as 0 (no HDR10 fallback), not as "not given" (review a09f6f5)
	i, _ = ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8,"dv_bl_signal_compatibility_id":0}]}],"format":{}}`))
	if v := i.Video(); v.DVCompat == nil || *v.DVCompat != 0 {
		t.Errorf("compat 0: %+v", v.DVCompat)
	}
	// ffprobe versions that print plain numbers (cd/m², 0..1) give the same values
	i, _ = ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","side_data_list":[{"side_data_type":"Mastering display metadata",
	 "red_x":0.68,"red_y":0.32,"green_x":0.265,"green_y":0.69,"blue_x":0.15,"blue_y":0.06,"white_point_x":0.3127,"white_point_y":0.329,"min_luminance":0.005,"max_luminance":1000}]}],"format":{}}`))
	if md := i.Video().MasteringDisplay; md != "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)" {
		t.Errorf("numbers: %s", md)
	}
	// an incomplete record is not written out
	i, _ = ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","side_data_list":[{"side_data_type":"Mastering display metadata","red_x":"34000/50000"}]}],"format":{}}`))
	if md := i.Video().MasteringDisplay; md != "" {
		t.Errorf("incomplete: %s", md)
	}
	// Dolby Vision from the MP4 sample entry alone (no record): the profile is not known
	for _, tag := range []string{"dvh1", "dvhe", "dav1"} {
		i, _ = ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","codec_tag_string":"` + tag + `"}],"format":{}}`))
		if v := i.Video(); !v.DolbyVision || v.DVProfile != 0 || v.DVCompat != nil {
			t.Errorf("%s: %+v", tag, v)
		}
	}
	if i, _ = ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","codec_tag_string":"hvc1"}],"format":{}}`)); i.IsHDR() {
		t.Error("hvc1 is not Dolby Vision")
	}
}

// The first frame carries what a TS file or raw stream has nowhere else: the Dolby Vision RPU, the
// mastering display, the light levels. Probe reads it for HEVC, AV1 and PQ/HLG video only.
func TestProbeReadsTheFirstFrame(t *testing.T) {
	frame := `{"frames":[{"side_data_list":[{"side_data_type":"Dolby Vision RPU Data"},
	 {"side_data_type":"Content light level metadata","max_content":800,"max_average":200}]}]}`
	for _, c := range []struct {
		stream string
		frames bool
	}{
		{`"codec_name":"hevc"`, true},
		{`"codec_name":"av1"`, true},
		{`"codec_name":"h264","color_transfer":"smpte2084"`, true},
		{`"codec_name":"h264","color_transfer":"arib-std-b67"`, true},
		{`"codec_name":"h264"`, false},
		{`"codec_name":"mpeg2video","color_transfer":"bt709"`, false},
	} {
		calls := 0
		x := &FakeExec{Handler: func(name string, args []string) (string, string, error) {
			calls++
			if strings.Contains(strings.Join(args, " "), "-show_frames") {
				if !strings.Contains(strings.Join(args, " "), "-select_streams v:1 -read_intervals %+#1") {
					t.Errorf("frame probe args %v", args) // the main video is the second (cover art first)
				}
				return frame, "", nil
			}
			return `{"streams":[{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}},{"codec_type":"video",` + c.stream + `}],"format":{}}`, "", nil
		}}
		i, err := Probe(context.Background(), x, "ffprobe", "/x.ts")
		if err != nil {
			t.Fatal(err)
		}
		v := i.Video()
		if got := calls == 2; got != c.frames || v.DolbyVision != c.frames || (c.frames && v.CLL != "800,200") || i.Streams[0].DolbyVision {
			t.Errorf("%s: %d calls, %+v", c.stream, calls, v)
		}
	}
	// the first frame can't be read: the file is not judged (a missed Dolby Vision would convert to wrong colours)
	x := &FakeExec{Handler: func(name string, args []string) (string, string, error) {
		if strings.Contains(strings.Join(args, " "), "-show_frames") {
			return "", "boom", context.DeadlineExceeded
		}
		return `{"streams":[{"codec_type":"video","codec_name":"hevc"}],"format":{}}`, "", nil
	}}
	if _, err := Probe(context.Background(), x, "ffprobe", "/x.ts"); err == nil {
		t.Error("a failed first-frame read must fail the probe")
	}
}
