// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import "sync/atomic"

// lowPriority is the Processing setting "low priority" (page 11, on by default): ffmpeg and ffprobe run
// below the machine's other work, so a conversion never makes the computer feel slow. The server sets it
// once when it starts; the settings slice will let the person turn it off.
var lowPriority atomic.Bool

// SetLowPriority turns low priority for started tools on or off. It is read when each tool starts.
func SetLowPriority(on bool) { lowPriority.Store(on) }

// LowPriority says whether started tools run at low priority.
func LowPriority() bool { return lowPriority.Load() }
