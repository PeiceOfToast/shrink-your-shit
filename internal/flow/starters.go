// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import "encoding/json"

// Starter is a workflow a person can start from before saving anything (page 05).
type Starter struct {
	ID          string
	Name        string
	Description string
	Flow        Flow
}

// Starters is the list the Start page offers. Stop 1 has one: "Shrink videos", the fixed starter
// the setup call already accepts (api's fixedStarterID). Its choices are workshop's, made 11-27
// for stop 1: HEVC at quality 3 on the processor, audio copied, an MKV next to the original with
// "-shrunk" added to its name.
func Starters() []Starter {
	steps := []StepRef{
		{ID: "video", Type: "video.convert", Version: 1,
			Settings: json.RawMessage(`{"format":"hevc","quality":3,"useGpu":false,"ifGpuFails":"cpu"}`)},
		{ID: "audio", Type: "audio.tracks", Version: 1,
			Settings: json.RawMessage(`{"format":"copy"}`)},
		{ID: "save", Type: "file.save", Version: 1,
			Settings: json.RawMessage(`{"container":"mkv","where":"next_to","nameSuffix":"shrunk"}`)},
	}
	return []Starter{{
		ID:          "shrink-videos",
		Name:        "Shrink videos",
		Description: "Smaller HEVC files next to the originals.",
		Flow: Flow{
			Name:        "Shrink videos",
			Description: "Smaller HEVC files next to the originals.",
			Steps:       steps,
		},
	}}
}

// StarterByID finds a starter by its id.
func StarterByID(id string) (Starter, bool) {
	for _, s := range Starters() {
		if s.ID == id {
			return s, true
		}
	}
	return Starter{}, false
}
