// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

// SkipWords says each skip code in plain words (COPY-1): the Preview's sample and the Skipped list.
var SkipWords = map[string]string{
	SkipAlreadyEfficient: "it is already stored efficiently, so converting it would save little",
	SkipAlreadyHEVC:      "it already uses a modern, efficient format",
	SkipAlreadyInFormat:  "it is already in the format, file type and audio you chose",                                                // a record without its plan's own words
	SkipModernFormat:     "it already uses a modern format (AV1 or VP9). Converting it would lose quality and usually make it bigger", // design, 23:33Z
	SkipDV:               dvSkip,
	SkipDVProfile5:       dv5Skip,
	SkipOtherFormat:      "its format is not one this setting converts",
	SkipNoVideo:          "it has no video",
	SkipUnknownBitrate:   "we can't tell how much it would shrink",
	SkipCannotKeepTrack:  "its subtitles could not be kept in its file type",
	SkipSettingCannotRun: "this setting can't convert files yet",
	SkipUnreadable:       "it can't be read",
	"restored":           "you put its original back, so it is left as it is",
	"user_stopped":       "you stopped it",
	SkipSharedFile:       "it is shared with another folder, so converting it would free no space",
	SkipSymlink:          "it is a link to a file outside this library, which is left as it is",
	// design's words (13:22Z); shown as "This file changed … next scan."
	SkipStillChanging: "this file changed since we last looked (it may still be downloading). We'll look at it again on the next scan",
}
