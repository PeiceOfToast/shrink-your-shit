// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// The guided builder's mapping (page 06, flow-model.md "Answers to flow"): one pure pair of
// functions, so the guide, its preview, the starters and setup's workflow can never disagree.
//
// FromAnswers makes the three steps from the answers. ToAnswers reads a flow back as answers and
// says ok only when FromAnswers would rebuild that same flow. Page 06's chips answer keys, the
// smaller sibling folder and the speed choice are the parts that need a rule of their own, and they
// are documented where each is read or written.

// ReplaceWhy is why page 06 greys "Replace the original" until stage 2 item 10, and why the Save
// step's check greys it. One sentence, so the guide and the editor say the same thing.
const ReplaceWhy = "Replacing the original comes in a later update; for now the converted file is saved next to it."

// besideSuffix ends the folder a beside_library answer writes: a sibling of the library root named
// "<library name> (smaller)". ToAnswers reads a folder ending in it as beside_library.
const besideSuffix = " (smaller)"

// guidedAudio is page 06's audio choices and the Audio settings each becomes: the format, the
// channels, and Advanced's codec when it replaces the format.
var guidedAudio = []struct {
	Answer   string
	Format   string
	Channels string
	Codec    string // empty: Advanced's codec stays unset
}{
	{"stereo_aac", "aac", "stereo", ""},
	{"stereo_aac_surround", "aac", "source", ""},
	{"eac3_surround", "aac", "source", "eac3"},
	{"ac3_surround", "aac", "source", "ac3"},
	{"stereo_opus", "opus", "stereo", ""},
	{"as_is", "copy", "source", ""},
}

// answerKeys are every key the guide asks, in the order page 06 draws its parts.
var answerKeys = []string{
	"format", "quality", "useGpu", "ifGpuFails", "resolution", "slower",
	"audio", "allLanguages", "languages", "languagesEverywhere",
	"subtitles", "subtitleLanguages", "fileType", "includeDolbyVision",
	"original", "folder",
}

// MergeAnswers fills the defaults in under an answers object. A caller's defaults may be nil: the
// guide's own defaults are the floor (a new workflow's original is next_to_original).
func MergeAnswers(answers, defaults Answers) Answers {
	out := Answers{}
	for k, v := range DefaultAnswers() {
		out[k] = v
	}
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range answers {
		out[k] = v
	}
	return out
}

// NormalizeAnswers is the answers object a save stores: the defaults filled in, only the keys the
// guide asks, and lists and numbers in the types the mapping reads. Two choices are canonicalized
// so a saved answers object is exactly what ToAnswers gives back: "every language" drops the
// language list (there is nothing to filter), and an empty list without it means the default
// languages ("my languages"). A nameSuffix is kept only when the flow adds one to the file name.
func NormalizeAnswers(answers, defaults Answers) Answers {
	merged := MergeAnswers(answers, defaults)
	out := Answers{}
	for _, key := range answerKeys {
		out[key] = merged[key]
	}
	// Lists become []string here: answers read back from JSON hold []any, and the mapping and the
	// column both want one shape. Empty, never nil, so nothing marshals to null.
	languages, _ := answerStrings(merged, "languages")
	if languages == nil {
		languages = []string{}
	}
	subtitleLanguages, _ := answerStrings(merged, "subtitleLanguages")
	if subtitleLanguages == nil {
		subtitleLanguages = []string{}
	}
	out["languages"], out["subtitleLanguages"] = languages, subtitleLanguages
	if out["folder"] == nil {
		out["folder"] = ""
	}
	allLanguages, _ := out["allLanguages"].(bool)
	if allLanguages {
		out["languages"] = []string{}
		out["languagesEverywhere"] = false
	} else if len(languages) == 0 {
		if base, _ := answerStrings(MergeAnswers(nil, defaults), "languages"); len(base) > 0 {
			out["languages"] = append([]string{}, base...)
		}
	}
	if suffix, ok := merged["nameSuffix"].(string); ok && suffix != "" {
		out["nameSuffix"] = suffix
	}
	return out
}

// FromAnswers turns the guided answers into the three fixed steps. It is pure: the same answers
// always make the same flow. A value that is not one the guide offers is an invalid_answer problem
// naming its key, and the page shows the first one on that field.
func FromAnswers(answers, defaults Answers) (Flow, []Problem) {
	a := MergeAnswers(answers, defaults)
	var problems []Problem
	bad := func(field, message string) {
		problems = append(problems, Problem{Field: field, Code: "invalid_answer", Message: message})
	}

	format, ok := answerString(a, "format")
	if !ok || !oneOf(format, "hevc", "h264", "av1") {
		bad("format", "Pick HEVC, H.264 or AV1.")
	}
	quality, ok := answerInt(a, "quality")
	if !ok || quality < 1 || quality > 5 {
		bad("quality", "Quality is 1 to 5.")
	}
	useGpu, ok := answerBool(a, "useGpu")
	if !ok {
		bad("useGpu", "Say whether the graphics card may be used.")
	}
	ifGpuFails, ok := answerString(a, "ifGpuFails")
	if !ok || !oneOf(ifGpuFails, "cpu", "stop") {
		bad("ifGpuFails", "Say what happens when the graphics card can't do it: use the processor or stop.")
	}
	resolution, ok := answerString(a, "resolution")
	if !ok || !oneOf(resolution, "keep", "1080", "720", "480") {
		bad("resolution", "Picture size must be the source's, 1080p, 720p or 480p.")
	}
	slower, ok := answerBool(a, "slower")
	if !ok {
		bad("slower", "Say whether the conversion should be slower and smaller.")
	}
	audio, ok := answerString(a, "audio")
	audioSet, audioOK := guidedAudioFor(audio)
	if !ok || !audioOK {
		bad("audio", "Pick one of the audio choices the guide offers.")
	}
	allLanguages, ok := answerBool(a, "allLanguages")
	if !ok {
		bad("allLanguages", "Say whether every language is kept.")
	}
	languages, ok := answerStrings(a, "languages")
	if !ok {
		bad("languages", "The languages must be a list of codes.")
	}
	languagesEverywhere, ok := answerBool(a, "languagesEverywhere")
	if !ok {
		bad("languagesEverywhere", "Say whether the languages are used everywhere.")
	}
	subtitles, ok := answerString(a, "subtitles")
	if !ok || !oneOf(subtitles, "keep_all", "my_languages", "text_only", "srt") {
		bad("subtitles", "Pick which subtitles to keep.")
	}
	subtitleLanguages, ok := answerStrings(a, "subtitleLanguages")
	if !ok {
		bad("subtitleLanguages", "The subtitle languages must be a list of codes.")
	}
	fileType, ok := answerString(a, "fileType")
	if !ok || !oneOf(fileType, "mkv", "mp4", "webm", "keep") {
		bad("fileType", "Pick MKV, MP4, WebM or Keep the same.")
	}
	includeDolbyVision, ok := answerBool(a, "includeDolbyVision")
	if !ok {
		bad("includeDolbyVision", "Say whether Dolby Vision is kept.")
	}
	original, ok := answerString(a, "original")
	if !ok || !oneOf(original, "replace", "next_to_original", "one_folder", "beside_library") {
		bad("original", "Pick where the new file goes.")
	}
	folder, ok := answerString(a, "folder")
	if !ok {
		bad("folder", "The folder must be text.")
	}
	nameSuffix, _ := answerString(a, "nameSuffix")
	if len(problems) > 0 {
		return Flow{}, problems
	}

	// The audio languages: all of them only when the person said so; otherwise their list, which
	// empty means my languages (page 07: the Audio step reads an empty list as the person's own).
	// The guide's own fallback is the defaults' list, the same canonical form NormalizeAnswers keeps.
	audioLanguages := languages
	if !allLanguages && len(audioLanguages) == 0 {
		audioLanguages, _ = answerStrings(MergeAnswers(nil, defaults), "languages")
	}
	if allLanguages {
		audioLanguages = []string{}
		// "Keep every language" and "use my languages everywhere" cannot both hold: with no
		// filter there is nothing to spread, so the answer only writes the audio step.
		languagesEverywhere = false
	}
	if languagesEverywhere {
		audioLanguages = append(append([]string{}, audioLanguages...), "orig")
		if subtitles == "my_languages" {
			subtitleLanguages = append([]string{}, audioLanguages...)
		}
	}

	size := any(nil)
	if resolution != "keep" {
		size = resolution
	}
	preset := "normal"
	if slower {
		preset = "slower"
	}
	video := map[string]any{
		"format": format, "quality": quality, "useGpu": useGpu, "ifGpuFails": ifGpuFails,
		"size": size, "preset": preset, "keepDolbyVision": includeDolbyVision,
	}
	audioSettings := map[string]any{
		"format": audioSet.Format, "channels": audioSet.Channels, "subtitles": subtitles,
	}
	// An empty list is left out, so the setting keeps its unset form: that is what "every language"
	// and "the audio's languages" mean to the engine, and it is the shape a starter or a person
	// clearing the picker already has.
	if len(audioLanguages) > 0 {
		audioSettings["languages"] = audioLanguages
	}
	if len(subtitleLanguages) > 0 {
		audioSettings["subtitleLanguages"] = subtitleLanguages
	}
	if audioSet.Codec != "" {
		audioSettings["codec"] = audioSet.Codec
	}
	where, saveFolder := "next_to", ""
	switch original {
	case "replace":
		where = "replace"
	case "one_folder", "beside_library":
		where, saveFolder = "folder", folder
	}
	save := map[string]any{
		"container": fileType, "where": where, "folder": saveFolder, "nameSuffix": nameSuffix,
	}
	return Flow{Steps: []StepRef{
		{ID: "video", Type: "video.convert", Version: 1, Settings: mustJSON(video)},
		{ID: "audio", Type: "audio.tracks", Version: 1, Settings: mustJSON(audioSettings)},
		{ID: "save", Type: "file.save", Version: 1, Settings: mustJSON(save)},
	}}, nil
}

// ToAnswers reads a flow back as the answers that would rebuild it. ok is true only when from those
// answers FromAnswers makes exactly this flow (the settings decode equal), so a flow the editor
// changed into something the guide cannot say comes back false and the page sends the person to 07.
func ToAnswers(f Flow) (Answers, bool) {
	decoded, err := decodedSteps(f)
	if err != nil {
		return nil, false
	}
	v, au, sv := decoded[0], decoded[1], decoded[2]

	// Video: the simple five, the speed choice, the picture size and Dolby Vision.
	format, ok := settingString(v, "format")
	if !ok || !oneOf(format, "hevc", "h264", "av1") {
		return nil, false
	}
	quality, ok := settingInt(v, "quality")
	if !ok || quality < 1 || quality > 5 {
		return nil, false
	}
	useGpu, ok := settingBool(v, "useGpu")
	if !ok {
		return nil, false
	}
	ifGpuFails, ok := settingString(v, "ifGpuFails")
	if !ok || !oneOf(ifGpuFails, "cpu", "stop") {
		return nil, false
	}
	preset, ok := optionalSettingString(v, "preset")
	if !ok || (preset != "" && preset != "normal" && preset != "slower") {
		return nil, false
	}
	size, ok := optionalSettingString(v, "size")
	if !ok || (size != "" && !oneOf(size, "1080", "720", "480")) {
		return nil, false
	}
	includeDolbyVision, ok := settingBool(v, "keepDolbyVision")
	if !ok {
		return nil, false
	}
	if !videoAdvancedIdle(v) {
		return nil, false
	}
	resolution := "keep"
	if size != "" {
		resolution = size
	}
	slower := preset == "slower"

	// Audio: the choice, the languages, the subtitles and nothing Advanced.
	audioAnswer, ok := guidedAudioAnswer(au)
	if !ok {
		return nil, false
	}
	if !audioAdvancedIdle(au) {
		return nil, false
	}
	audioLanguages, ok := settingStrings(au, "languages")
	if !ok {
		return nil, false
	}
	allLanguages, languagesEverywhere := false, false
	switch {
	case len(audioLanguages) == 0:
		allLanguages = true
		audioLanguages = []string{}
	case audioLanguages[len(audioLanguages)-1] == "orig":
		languagesEverywhere = true
		audioLanguages = audioLanguages[:len(audioLanguages)-1]
	}
	if audioLanguages == nil {
		audioLanguages = []string{}
	}
	subtitles, ok := settingString(au, "subtitles")
	if !ok || !oneOf(subtitles, "keep_all", "my_languages", "text_only", "srt") {
		return nil, false
	}
	subtitleLanguages, ok := settingStrings(au, "subtitleLanguages")
	if !ok {
		return nil, false
	}
	if subtitleLanguages == nil {
		subtitleLanguages = []string{}
	}
	// "Use my languages everywhere" writes the audio list, with orig on its end, into the subtitle
	// list too. The answers keep the person's own list, so take the added orig back off.
	if languagesEverywhere && subtitles == "my_languages" &&
		len(subtitleLanguages) > 0 && subtitleLanguages[len(subtitleLanguages)-1] == "orig" {
		subtitleLanguages = subtitleLanguages[:len(subtitleLanguages)-1]
		if subtitleLanguages == nil {
			subtitleLanguages = []string{}
		}
	}

	// Save: the file type, where the result goes, and the name suffix.
	fileType, ok := settingString(sv, "container")
	if !ok || !oneOf(fileType, "mkv", "mp4", "webm", "keep") {
		return nil, false
	}
	where, ok := settingString(sv, "where")
	if !ok {
		return nil, false
	}
	folder, ok := settingString(sv, "folder")
	if !ok {
		return nil, false
	}
	nameSuffix, ok := settingString(sv, "nameSuffix")
	if !ok {
		return nil, false
	}
	original := ""
	switch where {
	case "replace":
		original, folder = "replace", ""
	case "next_to":
		original, folder = "next_to_original", ""
	case "folder":
		if isBesideFolder(folder) {
			original = "beside_library"
		} else {
			original = "one_folder"
		}
	default:
		return nil, false
	}

	a := Answers{
		"format": format, "quality": quality, "useGpu": useGpu, "ifGpuFails": ifGpuFails,
		"resolution": resolution, "slower": slower,
		"audio": audioAnswer, "allLanguages": allLanguages, "languages": audioLanguages,
		"languagesEverywhere": languagesEverywhere,
		"subtitles":           subtitles, "subtitleLanguages": subtitleLanguages,
		"fileType": fileType, "includeDolbyVision": includeDolbyVision,
		"original": original, "folder": folder,
	}
	if nameSuffix != "" {
		a["nameSuffix"] = nameSuffix
	}
	// The round trip is the test: only an exact rebuild is a flow the guide can say. This catches
	// both an unmapped value and a mapping whose two directions disagree.
	back, problems := FromAnswers(a, DefaultAnswers())
	if len(problems) > 0 || !sameSteps(f, back) {
		return nil, false
	}
	return a, true
}

// Sentences is the guide's "What this does": numbered sentences in page 06's wording, from the flow
// the answers make. It shows what the run will do, not what the answers hoped for.
func Sentences(f Flow) []string {
	decoded, err := decodedSteps(f)
	if err != nil {
		return nil
	}
	v, au, sv := decoded[0], decoded[1], decoded[2]
	format, _ := settingString(v, "format")
	quality, _ := settingInt(v, "quality")
	size, _ := optionalSettingString(v, "size")
	useGpu, _ := settingBool(v, "useGpu")
	audioCodec, _ := optionalSettingString(au, "codec")
	if audioCodec == "" { // Advanced's codec unset: the format above is what is written
		audioCodec, _ = settingString(au, "format")
	}
	codecLabel := audioCodecName(audioCodec)
	channels, _ := settingString(au, "channels")
	languages, _ := settingStrings(au, "languages")
	subtitles, _ := settingString(au, "subtitles")
	subtitleLanguages, _ := settingStrings(au, "subtitleLanguages")
	container, _ := settingString(sv, "container")
	where, _ := settingString(sv, "where")
	folder, _ := settingString(sv, "folder")

	picture := "keeping the picture size"
	if size != "" {
		picture = "at most " + size + "p"
	}
	video := fmt.Sprintf("Converts video to %s at quality %d, %s", formatName(format), quality, picture)
	if useGpu {
		video += ", on the graphics card when it can"
	}
	video += "."
	audio := fmt.Sprintf("Audio: %s, %s", codecLabel, channelsName(channels))
	if names := languageNames(languages); names != "" {
		audio += ", in " + names
	} else {
		audio += ", every language"
	}
	audio += "."

	var sub string
	switch subtitles {
	case "srt":
		sub = "Subtitles are made into SRT files."
	case "text_only":
		sub = "Only text subtitles are kept."
	case "my_languages":
		names := languageNames(subtitleLanguages)
		if names == "" {
			names = "every language"
		}
		sub = "Keeps subtitles in " + names + "."
	default:
		sub = "Keeps every subtitle."
	}

	fileSentence := "The file keeps its container."
	if container != "keep" {
		fileSentence = "Saves as " + containerName(container) + "."
	}

	var place string
	switch where {
	case "replace":
		place = "Replaces the original. The old file goes to the trash and is kept 7 days."
	case "folder":
		if folder != "" {
			place = "Puts the new file in " + folder + "; the original stays where it is."
		} else {
			place = "Puts the new file in the folder you pick; the original stays where it is."
		}
	default:
		place = "Puts the new file next to the original; the original stays where it is."
	}
	return []string{video, audio, sub, fileSentence, place}
}

// decodedSteps decodes the three fixed steps of a flow, or refuses a flow of another shape.
func decodedSteps(f Flow) ([]Settings, error) {
	if err := CheckShape(f); err != nil {
		return nil, err
	}
	out := make([]Settings, 0, len(f.Steps))
	for _, ref := range f.Steps {
		_, settings, err := decodeStep(ref)
		if err != nil {
			return nil, err
		}
		out = append(out, settings)
	}
	return out, nil
}

// sameSteps reports whether two flows are the same three steps with settings that decode equal.
// Raw JSON is not compared: an absent field and one written at its default decode the same, and
// both are the same flow to the engine.
func sameSteps(a, b Flow) bool {
	da, err := decodedSteps(a)
	if err != nil {
		return false
	}
	db, err := decodedSteps(b)
	if err != nil {
		return false
	}
	for i := range da {
		if a.Steps[i].ID != b.Steps[i].ID || a.Steps[i].Type != b.Steps[i].Type {
			return false
		}
		if !sameSettings(da[i], db[i]) {
			return false
		}
	}
	return true
}

// sameSettings compares two decoded steps. A nil list and an empty one are the same setting: both
// mean "no filter", and writing [] and leaving the field out are the same file to the engine.
func sameSettings(a, b Settings) bool {
	return reflect.DeepEqual(emptyListsFor(a), emptyListsFor(b))
}

// emptyListsFor is a copy of a decoded step with every nil list made empty, so the comparison does
// not see the difference between the two ways of saying "nothing".
func emptyListsFor(s Settings) Settings {
	v := reflect.ValueOf(s)
	out := reflect.New(v.Type()).Elem()
	out.Set(v)
	for i := 0; i < out.NumField(); i++ {
		f := out.Field(i)
		if f.CanSet() && f.Kind() == reflect.Slice && f.IsNil() {
			f.Set(reflect.MakeSlice(f.Type(), 0, 0))
		}
	}
	return out.Interface()
}

// videoAdvancedIdle is true when every Advanced Video field is at its default, so the simple
// answers say the whole picture. Size, speed and Dolby Vision are the three the guide maps.
func videoAdvancedIdle(v Settings) bool {
	for _, field := range []string{"width", "height", "rateControl", "avgMbps", "maxMbps", "fps", "bitDepth"} {
		if value, ok := setting(v, field); !ok || value != nil {
			return false
		}
	}
	if twoPass, _ := settingBool(v, "twoPass"); twoPass {
		return false
	}
	if level, ok := settingString(v, "bitrateLevel"); !ok || level != "medium" {
		return false
	}
	if hdr, ok := optionalSettingString(v, "hdr"); !ok || (hdr != "" && hdr != "keep") {
		return false
	}
	return true
}

// audioAdvancedIdle is true when every Advanced Audio field and the subtitle-only fields are at
// their defaults, so the audio answer and the languages say the whole sound.
func audioAdvancedIdle(au Settings) bool {
	for _, field := range []string{"bitrateKbps", "sampleRate", "defaultTrack"} {
		if value, ok := setting(au, field); !ok || value != nil {
			return false
		}
	}
	if per, ok := settingString(au, "bitratePer"); !ok || per != "channel" {
		return false
	}
	if downmix, ok := optionalSettingString(au, "downmix"); !ok || (downmix != "" && downmix != "plain") {
		return false
	}
	if loudnorm, _ := settingBool(au, "loudnorm"); loudnorm {
		return false
	}
	if commentary, ok := settingString(au, "commentary"); !ok || commentary != "keep" {
		return false
	}
	if keep, _ := settingBool(au, "keepOriginal"); keep {
		return false
	}
	return true
}

// isBesideFolder reports the folder a beside_library answer writes: a sibling folder whose name
// ends in " (smaller)". ToAnswers cannot see the library, so that ending is the whole rule.
func isBesideFolder(folder string) bool {
	base := filepath.Base(folder)
	return strings.HasSuffix(base, besideSuffix) && len(base) > len(besideSuffix)
}

// guidedAudioFor finds page 06's audio choice and the settings it writes.
func guidedAudioFor(answer string) (struct {
	Answer   string
	Format   string
	Channels string
	Codec    string
}, bool) {
	for _, row := range guidedAudio {
		if row.Answer == answer {
			return row, true
		}
	}
	return guidedAudio[0], false
}

// guidedAudioAnswer is guidedAudioFor's other direction: the choice whose settings these are.
func guidedAudioAnswer(au Settings) (string, bool) {
	format, ok := settingString(au, "format")
	if !ok {
		return "", false
	}
	channels, ok := settingString(au, "channels")
	if !ok {
		return "", false
	}
	codec, ok := optionalSettingString(au, "codec")
	if !ok {
		return "", false
	}
	for _, row := range guidedAudio {
		if row.Format == format && row.Channels == channels && row.Codec == codec {
			return row.Answer, true
		}
	}
	return "", false
}

// mustJSON marshals one step's settings map. Every value in it is a string, number, bool or list,
// so the only error is a programming mistake.
func mustJSON(m map[string]any) json.RawMessage {
	raw, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("flow: guided settings do not marshal: %v", err))
	}
	return raw
}

func oneOf(value string, choices ...string) bool {
	for _, c := range choices {
		if value == c {
			return true
		}
	}
	return false
}

// The answers' values arrive from JSON (numbers are float64, lists are []any) or from Go tests
// (ints, []string). These read either shape and say whether the value was the right type at all.

func answerString(a Answers, key string) (string, bool) {
	s, ok := a[key].(string)
	return s, ok
}

func answerBool(a Answers, key string) (bool, bool) {
	b, ok := a[key].(bool)
	return b, ok
}

func answerInt(a Answers, key string) (int, bool) {
	switch v := a[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case json.Number:
		n, err := strconv.Atoi(v.String())
		return n, err == nil
	}
	return 0, false
}

func answerStrings(a Answers, key string) ([]string, bool) {
	switch v := a[key].(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// The decoded-settings readers: one set: tagged field by name, with a pointer's nil meaning unset.

// setting returns one field's decoded value; a nil pointer comes back as a nil any, so a caller can
// tell unset from a value.
func setting(s Settings, name string) (any, bool) {
	v := reflect.ValueOf(s)
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return nil, false
	}
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Tag.Get("set") != name {
			continue
		}
		f := v.Field(i)
		if f.Kind() == reflect.Pointer {
			if f.IsNil() {
				return nil, true
			}
			f = f.Elem()
		}
		switch f.Kind() {
		case reflect.String:
			return f.String(), true
		case reflect.Int:
			return int(f.Int()), true
		case reflect.Bool:
			return f.Bool(), true
		case reflect.Float64:
			return f.Float(), true
		case reflect.Slice:
			if list, ok := f.Interface().([]string); ok {
				return list, true
			}
		}
		return nil, false
	}
	return nil, false
}

func settingString(s Settings, name string) (string, bool) {
	value, ok := setting(s, name)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// optionalSettingString reads an unset pointer field as "", which is what every choice treats it as.
func optionalSettingString(s Settings, name string) (string, bool) {
	value, ok := setting(s, name)
	if !ok {
		return "", false
	}
	if value == nil {
		return "", true
	}
	text, ok := value.(string)
	return text, ok
}

func settingBool(s Settings, name string) (bool, bool) {
	value, ok := setting(s, name)
	if !ok {
		return false, false
	}
	b, ok := value.(bool)
	return b, ok
}

func settingInt(s Settings, name string) (int, bool) {
	value, ok := setting(s, name)
	if !ok {
		return 0, false
	}
	n, ok := value.(int)
	return n, ok
}

func settingStrings(s Settings, name string) ([]string, bool) {
	value, ok := setting(s, name)
	if !ok {
		return nil, false
	}
	list, ok := value.([]string)
	return list, ok
}

// formatName, audioCodecName, channelsName and containerName are page 06's words for the settings.

func formatName(format string) string {
	switch format {
	case "hevc":
		return "HEVC"
	case "h264":
		return "H.264"
	case "av1":
		return "AV1"
	}
	return format
}

func audioCodecName(codec string) string {
	switch codec {
	case "aac":
		return "AAC"
	case "opus":
		return "Opus"
	case "ac3":
		return "AC-3"
	case "eac3":
		return "E-AC-3"
	case "flac":
		return "FLAC"
	case "copy":
		return "Same as the source"
	}
	return codec
}

func channelsName(channels string) string {
	switch channels {
	case "stereo", "mono", "5.1":
		return channels
	}
	return "same channels as the source"
}

func containerName(container string) string {
	switch container {
	case "mkv":
		return "MKV"
	case "mp4":
		return "MP4"
	case "webm":
		return "WebM"
	}
	return container
}

// languageNames joins language codes the way the pages write them, "English and German". The
// special choices have their own words; an empty list is "".
func languageNames(codes []string) string {
	names := make([]string, 0, len(codes))
	for _, code := range codes {
		names = append(names, pageLanguageName(code))
	}
	return strings.Join(names, " and ")
}

func pageLanguageName(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	switch c {
	case "orig":
		return "Original language"
	case "und":
		return "Unknown"
	}
	for _, l := range Languages() {
		if l.Code == c {
			return l.Name
		}
	}
	return strings.TrimSpace(code)
}
