// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import "time"

// State is one state of a file, as FileState reads it: which file it is (device and inode, or the
// Windows volume and file index; "" where the system does not say), its size, its modification time and
// its creation and change times (Windows only; zero elsewhere, where ChangeTime reads the change time
// from the Lstat).
type State struct {
	Key     string
	Size    int64
	MTime   time.Time
	Born    time.Time
	Changed time.Time
}

// Same reports whether a and b are the same state of the same file: every field equal.
func (a State) Same(b State) bool {
	return a.Key == b.Key && a.Size == b.Size && a.MTime.Equal(b.MTime) && a.Born.Equal(b.Born) && a.Changed.Equal(b.Changed)
}
