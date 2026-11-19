// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import "errors"

// endChildren ends each of kids with stop, even when one fails or the walk that found them failed partway, and
// returns every error, the walk's last.
func endChildren(kids []int, walkErr error, stop func(int) error) error {
	var errs []error
	for _, k := range kids {
		if err := stop(k); err != nil {
			errs = append(errs, err)
		}
	}
	if walkErr != nil {
		errs = append(errs, walkErr)
	}
	return errors.Join(errs...)
}
