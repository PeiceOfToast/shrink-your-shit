// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// fakeStore is the record a crash leaves behind: the test fills it and reads what recovery did.
type fakeStore struct {
	list    []Sidecar
	dropped []string
	readErr error
}

func (f *fakeStore) AddSidecars(list []Sidecar) error { f.list = append(f.list, list...); return nil }
func (f *fakeStore) FinishSidecars(int64) error       { return nil }
func (f *fakeStore) Sidecars() ([]Sidecar, error)     { return f.list, f.readErr }

func (f *fakeStore) DropSidecar(jobID int64, path string) error {
	f.dropped = append(f.dropped, path)
	for i, c := range f.list {
		if c.JobID == jobID && c.Path == path {
			f.list = append(f.list[:i], f.list[i+1:]...)
			return nil
		}
	}
	return nil
}

// writeFile writes a file of this content.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// record is the record installSubtitles writes for path beside the new file videoPath (a video the
// test may not have placed yet).
func record(t *testing.T, jobID int64, path, videoPath string) Sidecar {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := fsx.SHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	c := Sidecar{JobID: jobID, Path: path, Bytes: st.Size(), MTime: st.ModTime().UnixNano(), Hash: h, VideoPath: videoPath}
	if vst, err := os.Stat(videoPath); err == nil {
		c.VideoBytes = vst.Size()
		if c.VideoHash, err = fsx.SampleHash(videoPath); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// TestSrtLeftByACrashIsRecovered: the .srt was placed and the new file was not, so the next start
// removes our file (it is still exactly the one we wrote) and its record goes.
func TestSrtLeftByACrashIsRecovered(t *testing.T) {
	dir := t.TempDir()
	srt := writeFile(t, dir, "movie.eng.srt", "1\n00:00:01,000 --> 00:00:02,000\nhi\n")
	store := &fakeStore{list: []Sidecar{record(t, 7, srt, filepath.Join(dir, "movie.mkv"))}}
	said := RecoverSidecars(store)
	if _, err := os.Stat(srt); err == nil {
		t.Error("recovery left the .srt of a file that was never placed")
	}
	if len(store.list) != 0 {
		t.Errorf("records left: %+v", store.list)
	}
	if len(said) != 1 || !strings.Contains(said[0], "removed") {
		t.Errorf("said = %v, want the removal", said)
	}
}

// TestSrtAfterASwapIsKept: the new file is in place, so its subtitles belong to it: only the record
// goes.
func TestSrtAfterASwapIsKept(t *testing.T) {
	dir := t.TempDir()
	video := writeFile(t, dir, "movie.mkv", "the new file")
	srt := writeFile(t, dir, "movie.eng.srt", "subtitles")
	store := &fakeStore{list: []Sidecar{record(t, 7, srt, video)}}
	RecoverSidecars(store)
	if _, err := os.Stat(srt); err != nil {
		t.Error("the subtitles of a placed file must stay")
	}
	if len(store.list) != 0 {
		t.Errorf("records left: %+v", store.list)
	}
}

// TestSrtCleanupKeepsAReplacedFile: an earlier output of ours that changed since is never removed;
// only its record goes.
func TestSrtCleanupKeepsAReplacedFile(t *testing.T) {
	dir := t.TempDir()
	srt := writeFile(t, dir, "movie.eng.srt", "subtitles")
	c := record(t, 7, srt, filepath.Join(dir, "movie.mkv"))
	writeFile(t, dir, "movie.eng.srt", "someone else's subtitles")
	store := &fakeStore{list: []Sidecar{c}}
	RecoverSidecars(store)
	b, err := os.ReadFile(srt)
	if err != nil || string(b) != "someone else's subtitles" {
		t.Errorf("a file that is not ours was removed or changed: %q %v", b, err)
	}
	if len(store.list) != 0 {
		t.Errorf("records left: %+v", store.list)
	}
}

// TestSrtSameSizeAndTimeButOtherContentSurvives: same size and modification time is still someone
// else's file, because the whole content is compared.
func TestSrtSameSizeAndTimeButOtherContentSurvives(t *testing.T) {
	dir := t.TempDir()
	srt := writeFile(t, dir, "movie.eng.srt", "subtitles")
	c := record(t, 7, srt, filepath.Join(dir, "movie.mkv"))
	st, _ := os.Stat(srt)
	if err := os.WriteFile(srt, []byte("subtitLEX"), 0o644); err != nil { // same length
		t.Fatal(err)
	}
	if err := os.Chtimes(srt, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	RecoverSidecars(&fakeStore{list: []Sidecar{c}})
	if b, _ := os.ReadFile(srt); string(b) != "subtitLEX" {
		t.Errorf("the other file was removed or changed: %q", b)
	}
}

// TestSrtChangedOutsideTheSamplesSurvives: a big .srt changed only between the parts a sampled hash
// reads is still told apart, because the whole content is compared.
func TestSrtChangedOutsideTheSamplesSurvives(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("a", 3<<20)
	srt := writeFile(t, dir, "movie.eng.srt", body)
	c := record(t, 7, srt, filepath.Join(dir, "movie.mkv"))
	st, _ := os.Stat(srt)
	b := []byte(body)
	b[1536<<10] ^= 0xff // between the first and the middle sample
	if err := os.WriteFile(srt, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(srt, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	RecoverSidecars(&fakeStore{list: []Sidecar{c}})
	if _, err := os.Stat(srt); err != nil {
		t.Error("a file that is not exactly ours was removed")
	}
}

// TestSrtRemovalFailureKeepsTheRecord: a file of ours that cannot be removed keeps its record, and
// the next start tries again.
func TestSrtRemovalFailureKeepsTheRecord(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the test cannot delete from (POSIX, not root)")
	}
	dir := t.TempDir()
	srt := writeFile(t, dir, "movie.eng.srt", "subtitles")
	store := &fakeStore{list: []Sidecar{record(t, 7, srt, filepath.Join(dir, "movie.mkv"))}}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	said := RecoverSidecars(store)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if len(store.list) != 1 || !strings.Contains(strings.Join(said, ";"), "could not remove") {
		t.Fatalf("the record must stay after a failed removal: %+v %v", store.list, said)
	}
	if _, err := os.Stat(srt); err != nil {
		t.Fatal("the file should still be there")
	}
	RecoverSidecars(store) // the next start
	if _, err := os.Stat(srt); err == nil {
		t.Error("the next recovery should remove it")
	}
	if len(store.list) != 0 {
		t.Errorf("records left: %+v", store.list)
	}
}

// TestSrtUncheckableKeepsTheRecord: when we cannot tell whether a recorded .srt is ours, the file
// is left alone and its record stays; once the check works again, the next start removes it.
func TestSrtUncheckableKeepsTheRecord(t *testing.T) {
	for _, fail := range []string{"lstat", "hash"} {
		t.Run(fail, func(t *testing.T) {
			dir := t.TempDir()
			srt := writeFile(t, dir, "movie.eng.srt", "subtitles")
			store := &fakeStore{list: []Sidecar{record(t, 7, srt, filepath.Join(dir, "movie.mkv"))}}
			ioErr := &os.PathError{Op: fail, Path: srt, Err: syscall.EIO}
			if fail == "lstat" {
				sidecarLstat = func(string) (os.FileInfo, error) { return nil, ioErr }
			} else {
				sidecarHash = func(string) (string, error) { return "", ioErr }
			}
			said := RecoverSidecars(store)
			sidecarLstat, sidecarHash = os.Lstat, fsx.SHA256
			if len(store.list) != 1 || !strings.Contains(strings.Join(said, ";"), "could not tell") {
				t.Fatalf("the record must stay when the file cannot be checked: %+v %v", store.list, said)
			}
			if _, err := os.Stat(srt); err != nil {
				t.Fatal("a file we could not check was removed")
			}
			RecoverSidecars(store) // the next start, with the check working
			if _, err := os.Stat(srt); err == nil {
				t.Error("the next recovery should remove it")
			}
			if len(store.list) != 0 {
				t.Errorf("records left: %+v", store.list)
			}
		})
	}
}

// TestSrtQuarantineKeepsAFileThatTakesTheName: a file that takes the recorded name in the instant
// our file is moved aside is not the file deleted; ours goes and the newcomer stays.
func TestSrtQuarantineKeepsAFileThatTakesTheName(t *testing.T) {
	dir := t.TempDir()
	srt := writeFile(t, dir, "movie.eng.srt", "subtitles")
	c := record(t, 7, srt, filepath.Join(dir, "movie.mkv"))
	sidecarRename = func(oldpath, newpath string) error {
		if err := fsx.RenameNoReplace(oldpath, newpath); err != nil {
			return err
		}
		// Another program takes the name the moment our file is moved aside.
		return os.WriteFile(oldpath, []byte("the newcomer"), 0o644)
	}
	t.Cleanup(func() { sidecarRename = fsx.RenameNoReplace })

	removed, err := RemoveSidecarIfOurs(c)
	if err != nil || !removed {
		t.Fatalf("removed = %v, %v; want our file moved aside and deleted", removed, err)
	}
	if b, err := os.ReadFile(srt); err != nil || string(b) != "the newcomer" {
		t.Errorf("the file that took the name was removed or changed: %q %v", b, err)
	}
	if _, err := os.Stat(srt + ".syp-quarantine-1"); err == nil {
		t.Error("the quarantine name was left behind after our file was deleted")
	}
}

// TestSrtQuarantineSkipsTakenNames: the move aside never goes over a file, even one left by an
// earlier removal.
func TestSrtQuarantineSkipsTakenNames(t *testing.T) {
	dir := t.TempDir()
	srt := writeFile(t, dir, "movie.eng.srt", "subtitles")
	c := record(t, 7, srt, filepath.Join(dir, "movie.mkv"))
	writeFile(t, dir, "movie.eng.srt.syp-quarantine-1", "an older file")
	removed, err := RemoveSidecarIfOurs(c)
	if err != nil || !removed {
		t.Fatalf("removed = %v, %v; want our file moved aside and deleted", removed, err)
	}
	if b, _ := os.ReadFile(srt + ".syp-quarantine-1"); string(b) != "an older file" {
		t.Errorf("the file already at a quarantine name was removed or changed: %q", b)
	}
}

// TestSrtPutBackWhenTheNameIsTaken: a file that is not ours is put back at its name; when another
// file took that name meanwhile, it stays under the quarantine name and the record stays, so
// nothing that is not ours is ever deleted.
func TestSrtPutBackWhenTheNameIsTaken(t *testing.T) {
	dir := t.TempDir()
	srt := writeFile(t, dir, "movie.eng.srt", "subtitles")
	c := record(t, 7, srt, filepath.Join(dir, "movie.mkv"))
	if err := os.WriteFile(srt, []byte("someone else's subtitles"), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecarRename = func(oldpath, newpath string) error {
		if err := fsx.RenameNoReplace(oldpath, newpath); err != nil {
			return err
		}
		if oldpath == srt {
			return os.WriteFile(oldpath, []byte("the newcomer"), 0o644)
		}
		return nil
	}
	t.Cleanup(func() { sidecarRename = fsx.RenameNoReplace })

	store := &fakeStore{list: []Sidecar{c}}
	said := RecoverSidecars(store)
	if len(store.list) != 1 || !strings.Contains(strings.Join(said, ";"), "could not be put back") {
		t.Fatalf("the record must stay when the file cannot go back: %+v %v", store.list, said)
	}
	if b, _ := os.ReadFile(srt); string(b) != "the newcomer" {
		t.Errorf("the file that took the name was removed or changed: %q", b)
	}
	if b, err := os.ReadFile(srt + ".syp-quarantine-1"); err != nil || string(b) != "someone else's subtitles" {
		t.Errorf("the file moved aside must be kept where it is: %q %v", b, err)
	}
}

// TestSrtRecordsUnreadableIsSaid: a store that cannot be read is reported, not silently ignored.
func TestSrtRecordsUnreadableIsSaid(t *testing.T) {
	said := RecoverSidecars(&fakeStore{readErr: errors.New("database is busy")})
	if len(said) != 1 || !strings.Contains(said[0], "database is busy") {
		t.Errorf("said = %v, want the store's error", said)
	}
}

// TestFreeName: the name Place would use now, so the .srt names can match it.
func TestFreeName(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "movie.mkv")
	if got, err := FreeName(final); err != nil || got != final {
		t.Fatalf("free name = %q %v, want %q", got, err, final)
	}
	writeFile(t, dir, "movie.mkv", "first")
	if got, _ := FreeName(final); got != filepath.Join(dir, "movie (2).mkv") {
		t.Errorf("taken name = %q, want movie (2).mkv", got)
	}
	writeFile(t, dir, "movie (2).mkv", "second")
	if got, _ := FreeName(final); got != filepath.Join(dir, "movie (3).mkv") {
		t.Errorf("twice-taken name = %q, want movie (3).mkv", got)
	}
}
