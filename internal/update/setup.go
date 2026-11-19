// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// SetupName is the Windows installer of a release. Its checksum is in the release's checksums.txt.
const SetupName = "shrink-your-shit-setup.exe"

// FetchSetup downloads release version's Windows installer to dest, after checking its SHA-256 against the
// release's checksums.txt. A mismatch is refused and the download is deleted; dest is either complete or absent.
func FetchSetup(ctx context.Context, client *http.Client, base, version, dest string) error {
	if !ValidVersion(version) {
		return fmt.Errorf("%q is not a release version", version)
	}
	dir := strings.TrimSuffix(base, "/") + "/v" + version + "/"
	sums, err := get(ctx, client, dir+"checksums.txt", nil)
	if err != nil {
		return err
	}
	want := checksumFor(sums, SetupName)
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", SetupName)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".syp-setup-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	if _, err := get(ctx, client, dir+SetupName, io.MultiWriter(tmp, h)); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s); the download was deleted", SetupName, want, got)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}
