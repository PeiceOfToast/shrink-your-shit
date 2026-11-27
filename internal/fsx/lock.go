// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import "errors"

// ErrLocked is what Lock returns when another program already holds the lock (the lock half of
// L-SAFE-17: one program per data folder).
var ErrLocked = errors.New("the file is locked by another program")
