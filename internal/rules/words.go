// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

// SkipWords says each skip code in plain words (COPY-1): the Preview's sample and the Skipped list.
var SkipWords = map[string]string{
	SkipAlreadyEfficient: "it is already stored efficiently, so converting it would save little",
	SkipAlreadyHEVC:      "it already uses a modern, efficient format",
	SkipOtherFormat:      "its format is not one this setting converts",
	SkipNoVideo:          "it has no video",
	SkipUnknownBitrate:   "we can't tell how much it would shrink",
	SkipCannotKeepTrack:  "its subtitles could not be kept in its file type",
	SkipSettingCannotRun: "this setting can't convert files yet",
	SkipUnreadable:       "it can't be read",
	"restored":           "you put its original back, so it is left as it is",
}
