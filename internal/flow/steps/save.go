// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// SaveSettings is page 07's Save card. Next to the original is the only place the engine writes
// to until item 10 builds replacing and a chosen folder, which Validate reports.
type SaveSettings struct {
	Container  string `set:"container" label:"File type" choices:"mkv=MKV,mp4=MP4,webm=WebM,keep=Keep the same" default:"mkv" help:"MKV holds every track and subtitle. MP4 plays on more devices but drops some subtitles."`
	Where      string `set:"where" label:"Where it goes" choices:"replace=Replace the original,next_to=Next to the original,folder=A different folder" default:"next_to" notYet:"replace=Replacing the original comes in a later update; for now the new file is saved next to it.,folder=Saving to a different folder comes in a later update; for now the new file is saved next to it."`
	Folder     string `set:"folder" label:"Folder" widget:"text" showIf:"where=folder"`
	NameSuffix string `set:"nameSuffix" label:"Add to the file name" default:"" help:"Added after a hyphen: media.mp4 becomes media-small.mp4. Empty keeps the name."`
}

func init() { flow.RegisterStep(SaveStep{}) }

// SaveStep names the container and the output path. It never writes anything: the engine stages,
// checks and places the file with safety.
type SaveStep struct{}

var _ flow.Step = SaveStep{}

// ContainerChoice is one of the settings facts the flow's container rules read
// (flow/containers.go): the file type this step asks for.
func (set SaveSettings) ContainerChoice() string { return set.Container }

// Def is the step's own description.
func (SaveStep) Def() flow.Definition {
	return flow.Definition{
		Type:     "file.save",
		Version:  1,
		Name:     "Save",
		Help:     "Choose the file type and the name the new file gets. The original is never overwritten.",
		Settings: SaveSettings{},
		Events: []flow.EventDef{
			{Code: "save.next_to", Level: events.LevelInfo, Text: "Saving next to the original",
				Fields: []string{"container", "output"}, Keep: events.KeepLog},
			{Code: "save.nothing", Level: events.LevelInfo, Text: "Nothing to save",
				Keep: events.KeepLog},
			{Code: "save.would_land_on_source", Level: events.LevelError,
				Text: "The new file would take the original's name", Keep: events.KeepLog},
			{Code: "save.unknown_container", Level: events.LevelError,
				Text: "The original's file type has no match here", Keep: events.KeepLog},
		},
	}
}

// Plan names the output. It decides "nothing" when no other step is changing the file, so a flow
// whose video is already the target leaves the file alone instead of writing an identical copy.
func (SaveStep) Plan(s flow.Settings, in flow.PlanInput, p *encode.Plan) (flow.Decision, error) {
	set := s.(SaveSettings)
	container, err := containerFor(set.Container, in.Info)
	if err != nil {
		return flow.Decision{}, err
	}
	output := outputPath(in.Info.Path, set.NameSuffix, container)
	if sameFile(output, in.Info.Path) {
		return flow.Decision{}, &flow.PlanError{
			Code:    "save.would_land_on_source",
			Message: "The new file would take the original's name; add a suffix.",
		}
	}
	p.Container = container
	p.Output = output

	// Only the picture can change in stage 1; the audio step copies. When the video step said
	// "nothing", there is nothing worth writing.
	if p.Video.Codec == "" || p.Video.Codec == encode.CodecCopy {
		return flow.Decision{Work: false, Label: "Nothing to save", Reason: "save.nothing",
			Detail: flow.F{"output": output}}, nil
	}
	return flow.Decision{
		Work: true, Label: "Saving next to the original as " + strings.ToUpper(string(container)),
		Reason: "save.next_to",
		Detail: flow.F{"container": string(container), "output": output},
	}, nil
}

// Validate refuses what stage 1 cannot do and a suffix the cleaner empties. The source file is
// not known here, so a check that the result would not land on the original is made by Plan.
func (SaveStep) Validate(s flow.Settings, vc flow.ValidateContext) flow.Check {
	set := s.(SaveSettings)
	var c flow.Check
	c.Problems = append(c.Problems, flow.TagProblems(SaveStep{}.Def(), set)...)
	// Replacing the original and a chosen folder arrive with item 10. The form greys both choices
	// with these sentences from the notYet tag; the check greys them here with the same words, and a
	// stored value is still a problem below, with its own sentence, rather than a silent fall back
	// to next-to.
	c.Disabled = append(c.Disabled,
		flow.Disabled{Field: "where", Value: "replace", Why: replaceWhy},
		flow.Disabled{Field: "where", Value: "folder", Why: folderWhy})
	switch set.Where {
	case "replace":
		c.Problems = append(c.Problems, flow.Problem{
			Field: "where", Code: "save.not_yet", Message: replaceWhy,
		})
	case "folder":
		c.Problems = append(c.Problems, flow.Problem{
			Field: "where", Code: "save.not_yet", Message: folderWhy,
		})
	}
	if set.Where == "folder" && strings.TrimSpace(set.Folder) == "" {
		c.Problems = append(c.Problems, flow.Problem{
			Field: "folder", Code: "save.folder_missing",
			Message: "Choose the folder the new file goes in.",
		})
	}
	// What the picked file type can hold is the flow's rule, not this step's
	// (flow/containers.go): it needs Video's format and Audio's codec too.
	// Only "keep" guarantees the output has the source's extension. With an explicit container the
	// source may be another file type, so an empty suffix is not a problem by itself; Plan refuses
	// the one source type whose name it would really take.
	if set.Container == "keep" && set.NameSuffix != "" && cleanSuffix(set.NameSuffix) == "" {
		c.Problems = append(c.Problems, flow.Problem{
			Field: "nameSuffix", Code: "save.empty_suffix",
			Message: "The suffix has no characters a file name can keep, so the new file would take the original's name.",
		})
	}

	c.Summary = saveSummary(set)
	return c
}

// replaceWhy is why the page greys "Replace the original" until item 10, and folderWhy why it
// greys "A different folder". The where field's notYet tag repeats both for the form, so
// TestSaveChoicesAreGreyed can compare them; the guide's greyed choices read the same constants,
// so the two pages cannot drift apart.
const (
	replaceWhy = flow.ReplaceWhy
	folderWhy  = flow.FolderWhy
)

// trashDays is how long a replaced original stays in the trash. Page 11's
// processing.keepOriginalsDays is the app-wide setting; until that page feeds the check, the
// summary carries the page's own number.
const trashDays = 7

// saveSummary is the closed card's line in page 07's words: the file type, then where the result
// goes. It starts with the file type, as the page draws it.
func saveSummary(set SaveSettings) string {
	return containerLabel(set.Container) + " · " + whereText(set.Where, set.Folder)
}

// containerFor resolves the file-type choice; "keep" is the source's own container.
func containerFor(choice string, info media.Info) (encode.Container, error) {
	switch choice {
	case "mkv":
		return encode.ContainerMKV, nil
	case "mp4":
		return encode.ContainerMP4, nil
	}
	names := info.Format.Names
	switch {
	// ffprobe calls both Matroska and WebM "matroska,webm", so the extension decides which one
	// the file really is.
	case slices.Contains(names, "webm") && strings.EqualFold(filepath.Ext(info.Path), ".webm"):
		return encode.ContainerWebM, nil
	case slices.Contains(names, "matroska"):
		return encode.ContainerMKV, nil
	case slices.Contains(names, "mp4"), slices.Contains(names, "mov"):
		return encode.ContainerMP4, nil
	}
	return "", &flow.PlanError{
		Code:    "save.unknown_container",
		Message: "The original's file type has no match here; choose MKV or MP4.",
	}
}

// outputPath is <folder of the source>/<stem>[-<suffix>].<ext>.
func outputPath(source, suffix string, container encode.Container) string {
	base := filepath.Base(source)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	name := stem
	if cleaned := cleanSuffix(suffix); cleaned != "" {
		name += "-" + cleaned
	}
	return filepath.Join(filepath.Dir(source), name+"."+string(container))
}

// cleanSuffix drops the characters a file name cannot hold on the platforms this runs on, and
// the leading and trailing spaces and dots Windows also refuses.
func cleanSuffix(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), " .")
}

// sameFile compares two paths the way the platform does.
func sameFile(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func containerLabel(choice string) string {
	switch choice {
	case "mkv":
		return "MKV"
	case "mp4":
		return "MP4"
	case "webm":
		return "WebM"
	case "keep":
		return "Keep the same"
	}
	return choice
}

func whereText(where, folder string) string {
	switch where {
	case "replace":
		return fmt.Sprintf("replace the original · old file in the trash %d days", trashDays)
	case "folder":
		if folder != "" {
			return "a different folder: " + folder
		}
		return "a different folder"
	}
	return "next to the original"
}
