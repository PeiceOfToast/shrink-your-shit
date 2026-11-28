// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import "encoding/json"

// Starter is a workflow a person can start from before saving anything (page 05).
type Starter struct {
	ID string
	// Kind is the workflow kind the starter makes: "video" in stage 1.
	Kind string
	Name string
	// Description is the workflow's description, kept with the workflow when it is made.
	Description string
	// Line is the one sentence the Start page's card shows under the name.
	Line string
	// Icon names the card's icon.
	Icon string
	// Guided is true for the starters the first-run page offers.
	Guided bool
	Flow   Flow
}

// Starters is the list the Start page offers. Stop 1 has one: "Shrink videos", the fixed starter
// the setup call already accepts by id. Its choices are workshop's, made 11-27
// for stop 1: HEVC at quality 3 on a graphics card when the worker has one (and on the processor
// when it does not), audio copied, an MKV next to the original with "-shrunk" added to its name.
func Starters() []Starter {
	steps := []StepRef{
		{ID: "video", Type: "video.convert", Version: 1,
			Settings: json.RawMessage(`{"format":"hevc","quality":3,"useGpu":true,"ifGpuFails":"cpu"}`)},
		{ID: "audio", Type: "audio.tracks", Version: 1,
			Settings: json.RawMessage(`{"format":"copy"}`)},
		{ID: "save", Type: "file.save", Version: 1,
			Settings: json.RawMessage(`{"container":"mkv","where":"next_to","nameSuffix":"shrunk"}`)},
	}
	return []Starter{{
		ID:          "shrink-videos",
		Kind:        "video",
		Name:        "Shrink videos",
		Description: "Smaller HEVC files next to the originals.",
		Line:        "Answer a few plain questions. The steps are set for you, and you can open them in the editor later.",
		Icon:        "flow",
		Guided:      true,
		Flow: Flow{
			Name:        "Shrink videos",
			Description: "Smaller HEVC files next to the originals.",
			Steps:       steps,
		},
	}}
}

// StarterAnswers is the answers a starter opens on (page 05's card, page 06 already answered): its
// own when the guide can say them, else the guide's defaults.
func StarterAnswers(st Starter) Answers {
	if st.Guided {
		if own, fits := ToAnswers(st.Flow); fits {
			return own
		}
	}
	return DefaultAnswers()
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
