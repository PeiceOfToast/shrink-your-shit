// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

// The container rules cross steps: whether the file type Save asks for can hold the video format
// Video asks for and the audio codec Audio will write (page 07, "Pairs a file type can't hold").
// They live here rather than in a step because no one step knows the other two.
//
// The wording is the editor's own table: ui/src/api/mockFlow.ts CONTAINER_RULES, so the live check
// and the page say the same sentence.

// The settings facts these rules read. A settings value that does not implement one is left out
// of the rules, which is what an older or foreign shape should be.
type videoFormat interface{ VideoFormat() string }
type audioCodec interface{ AudioCodec() string }
type containerChoice interface{ ContainerChoice() string }

// containerRules is every file type that can be ruled out, with why and with what it can hold. MKV
// and Keep the same take anything, so they have no entry. A new rule goes here, not in a step.
var containerRules = []struct {
	Value string
	Why   string
	OK    func(video, audio string) bool
}{
	{Value: "mp4", Why: "MP4 can't hold Opus audio.", OK: func(_, audio string) bool {
		return audio != "opus"
	}},
	{Value: "webm", Why: "WebM needs AV1 video and Opus audio.", OK: func(video, audio string) bool {
		return video == "av1" && audio == "opus"
	}},
}

// containerFacts is what the three steps said that the rules need.
func containerFacts(settings []Settings) (video, audio, chosen string, ok bool) {
	var haveVideo, haveAudio, haveSave bool
	for _, s := range settings {
		if v, is := s.(videoFormat); is {
			video, haveVideo = v.VideoFormat(), true
		}
		if a, is := s.(audioCodec); is {
			audio, haveAudio = a.AudioCodec(), true
		}
		if c, is := s.(containerChoice); is {
			chosen, haveSave = c.ContainerChoice(), true
		}
	}
	return video, audio, chosen, haveVideo && haveAudio && haveSave
}

// containerDisabled greys each file type the picked video and audio cannot go into.
func containerDisabled(settings []Settings) []Disabled {
	video, audio, _, ok := containerFacts(settings)
	if !ok {
		return nil
	}
	var out []Disabled
	for _, rule := range containerRules {
		if rule.OK(video, audio) {
			continue
		}
		out = append(out, Disabled{StepID: "save", Field: "container", Value: rule.Value, Why: rule.Why})
	}
	return out
}

// containerProblems refuses the file type Save already has when the picked video and audio cannot
// go into it.
func containerProblems(settings []Settings) []Problem {
	video, audio, chosen, ok := containerFacts(settings)
	if !ok {
		return nil
	}
	var out []Problem
	for _, rule := range containerRules {
		if rule.Value != chosen || rule.OK(video, audio) {
			continue
		}
		out = append(out, Problem{
			StepID: "save", Field: "container", Code: "container_unsupported",
			Message: rule.Why + " Pick another file type.",
		})
	}
	return out
}
