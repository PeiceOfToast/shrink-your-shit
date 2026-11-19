// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ReleaseBase is where release files are downloaded from: <base>/v<version>/<file>.
const ReleaseBase = "https://github.com/PeiceOfToast/shrink-your-shit/releases/download"

// maxDownload caps an archive or checksums.txt (a release archive is about 30 MB).
const maxDownload = 512 << 20

// maxProgram caps the program taken out of an archive; a larger one is refused, not cut short.
var maxProgram int64 = maxDownload

var strictVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)

// ValidVersion reports whether v is a plain x.y.z release number (no "v", no suffix, no leading zeros).
func ValidVersion(v string) bool { return strictVersion.MatchString(v) }

// Newer reports whether latest is a later release than current ("dev" or anything unreadable never is).
func Newer(latest, current string) bool { return newer(latest, current) }

// ArchiveName is the release archive for a platform, as scripts/build-release.sh names it.
func ArchiveName(goos, goarch string) string {
	return "shrink-your-shit_" + goos + "_" + goarch + ".tar.gz"
}

// Fetch downloads release version's archive for goos/goarch and checksums.txt from base, checks the
// archive's SHA-256 against checksums.txt and writes only the program inside it to dest (mode 0755).
// Nothing is left behind on any failure: a mismatched download is deleted and dest is not created.
func Fetch(ctx context.Context, client *http.Client, base, version, goos, goarch, dest string) error {
	if !ValidVersion(version) {
		return fmt.Errorf("%q is not a release version", version)
	}
	name := ArchiveName(goos, goarch)
	dir := strings.TrimSuffix(base, "/") + "/v" + version + "/"
	sums, err := get(ctx, client, dir+"checksums.txt", nil)
	if err != nil {
		return err
	}
	want := checksumFor(sums, name)
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".syp-download-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // the archive is never kept
	defer tmp.Close()
	h := sha256.New()
	if _, err := get(ctx, client, dir+name, io.MultiWriter(tmp, h)); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s); the download was deleted", name, want, got)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	exe := "shrink-your-shit"
	if goos == "windows" {
		exe += ".exe"
	}
	return extract(tmp, strings.TrimSuffix(name, ".tar.gz")+"/"+exe, dest)
}

// get downloads url. With w nil it returns the body (for checksums.txt); otherwise it copies into w.
func get(ctx context.Context, client *http.Client, url string, w io.Writer) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "shrink-your-shit-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, maxDownload)
	if w == nil {
		return io.ReadAll(body)
	}
	_, err = io.Copy(w, body)
	if err != nil {
		err = fmt.Errorf("download %s: %v", url, err)
	}
	return nil, err
}

// checksumFor finds name's hash in a sha256sum listing ("<hex>  <name>" lines); "" if it is not there.
func checksumFor(sums []byte, name string) string {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// extract writes the archive's regular file entry (exactly that path) to dest, through a temporary file
// in dest's folder, so dest is either complete or absent.
func extract(r io.Reader, entry, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("the archive could not be read: %v", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the archive has no %s", entry)
		}
		if err != nil {
			return fmt.Errorf("the archive could not be read: %v", err)
		}
		if h.Name != entry || h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Size > maxProgram {
			return fmt.Errorf("the program in the archive is %d bytes, more than the %d allowed", h.Size, maxProgram)
		}
		out, err := os.CreateTemp(filepath.Dir(dest), ".syp-new-")
		if err != nil {
			return err
		}
		_, cerr := io.Copy(out, tr) // tar gives exactly h.Size bytes
		err = errors.Join(cerr, out.Chmod(0o755), out.Close())
		if err == nil {
			err = os.Rename(out.Name(), dest)
		}
		if err != nil {
			os.Remove(out.Name())
		}
		return err
	}
}
