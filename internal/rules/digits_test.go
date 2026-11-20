// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "testing"

// log-spec R2: sizes in bytes carry digit groups, so 4012345678 bytes reads as "4,012,345,678 bytes".
func TestGroupDigits(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 4012345678: "4,012,345,678", -1234: "-1,234"} {
		if got := GroupDigits(n); got != want {
			t.Errorf("GroupDigits(%d) = %q, want %q", n, got, want)
		}
	}
}
