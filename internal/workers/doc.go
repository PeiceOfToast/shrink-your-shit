// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package workers keeps each worker's own settings: the files it runs at once, the places it may take
// on the graphics card, the hours it may work, and the encoder it prefers (page 10). Stage 3 has one
// built-in worker, "local"; remote workers and their libraries come later.
package workers
