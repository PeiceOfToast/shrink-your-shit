// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Keeping originals (slice 17, QOL-SAFETY-PLAN.md S2): in replace mode the original is moved into a
// hidden .syp-trash folder beside it (a rename on the same disk: instant, no copy) and kept for some
// days; the new file then takes its name. The swap is journaled: its row is committed before either
// rename, so after a crash or power cut RecoverSwaps leaves either the original or the new file in
// place, never neither and never a half state.

// TrashDir is the folder beside each converted file that holds its kept original. Hidden, so the
// scanner, the Preview and the setup checks never look inside it.
const TrashDir = ".syp-trash"

// Journal is where swaps are recorded (the job store).
type Journal interface {
	BeginSwap(o jobs.Original) (int64, error)
	FinishSwap(id int64, deleteOn time.Time) error
	SetSwapState(id int64, state string) error
	Originals(state string) ([]jobs.Original, error)
	CompleteKeptJobs() (int64, error)
}

// swapHook, in tests, runs at each step of a swap; an error from it stops the swap right there, the
// way a crash would (nothing after it runs, nothing is cleaned up by the swap).
var swapHook func(step string) error

// errCrash is what a swapHook returns to simulate a crash.
var errCrash = errors.New("simulated crash")

func hook(step string) error {
	if swapHook != nil {
		return swapHook(step)
	}
	// Crash testing on a real machine: SYP_TEST_SWAP_PAUSE=<step> stops the swap at that step for a
	// minute, after saying so in the log, so a kill -9 lands exactly there. Unset: nothing happens.
	if p := os.Getenv("SYP_TEST_SWAP_PAUSE"); p != "" && p == step {
		log.Printf("swap: paused at %q (SYP_TEST_SWAP_PAUSE)", step)
		time.Sleep(time.Minute)
	}
	return nil
}

// SampleHash identifies a file's content cheaply, also for a 50 GB file: SHA-256 over its size and
// three 1 MiB samples (start, middle, end). Slice 18 uses it to check a restored original; a full
// hash would read the whole file on every conversion.
func SampleHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	const chunk = 1 << 20
	size := st.Size()
	h := sha256.New()
	binary.Write(h, binary.BigEndian, size)
	for _, at := range []int64{0, size/2 - chunk/2, size - chunk} {
		if at < 0 {
			at = 0
		}
		if _, err := io.Copy(h, io.NewSectionReader(f, at, min(chunk, size-at))); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// trashPath is a free name for the original of job id in dir's .syp-trash: "<name>.<job id><ext>",
// with ".2", ".3"... added if that is taken (never overwrite a kept original). Only a name that exists
// moves on to the next; any other answer (the folder cannot be searched, a disk error) is an error, so a
// folder the program may not look into never makes it loop.
func trashPath(dir, base string, id int64) (string, error) {
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext) + "." + strconv.FormatInt(id, 10)
	p := filepath.Join(dir, stem+ext)
	for n := 2; n < 10000; n++ {
		_, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return p, nil
		case err != nil:
			return "", err
		}
		p = filepath.Join(dir, stem+"."+strconv.Itoa(n)+ext)
	}
	return "", fmt.Errorf("too many kept originals named %s", stem)
}

// keepAndReplace moves the original of job into .syp-trash and the checked new file (stage) to its
// name, journaled. stillOK is called right before each rename (the original and the new file must
// still be the ones that were read and checked). On any refusal or error the original is back in place.
//
// dest is where the new file goes: job.Path, or another name when the file type changes (25a-2). Then
// the new file never replaces anything at dest (renameNoReplace), the original goes back with
// renameNoReplace if that fails, and with 0 days the kept original is deleted right after the swap.
func (w *Worker) keepAndReplace(ctx context.Context, job jobs.Job, stage *staged, before os.FileInfo, dest string, days int, stillOK func() error) error {
	renaming := dest != job.Path
	dir := filepath.Join(filepath.Dir(job.Path), TrashDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("folder for kept originals %s: %v", dir, err)
		if errors.Is(err, fs.ErrPermission) {
			// The library folder refuses a new entry (it turned read-only after the encode): a failed
			// replace step whose key line names .syp-trash, so the person's sentence names its folder
			// (jobs.DeniedFolder), not a safety check that said no (manager's job 7).
			return err
		}
		return keptUnwritable("the folder that keeps originals couldn't be made here, so the original was left as it is. Check that shrink-your-shit may write in this folder")
	}
	hash, err := SampleHash(job.Path)
	if err != nil {
		return fmt.Errorf("reading the original to record it: %w", err)
	}
	trash, err := trashPath(dir, filepath.Base(job.Path), job.ID)
	if err != nil {
		log.Printf("folder for kept originals %s: %v", dir, err)
		return keptUnwritable("the folder that keeps originals couldn't be used, so the original was left as it is. Check that shrink-your-shit may write in this folder")
	}
	nst, err := os.Stat(stage.path)
	if err != nil {
		return fmt.Errorf("the new file: %w", err)
	}
	newHash, err := SampleHash(stage.path)
	if err != nil {
		return fmt.Errorf("the new file: %w", err)
	}
	o := jobs.Original{JobID: job.ID, LibraryID: job.LibraryID, Path: job.Path, Trash: trash,
		Staged: stage.path, Bytes: before.Size(), ModTime: before.ModTime().UnixNano(), Hash: hash, NewBytes: nst.Size(),
		NewMTime: nst.ModTime().UnixNano(), NewHash: newHash}
	if renaming {
		o.NewPath = dest
	}
	id, err := w.Journal.BeginSwap(o)
	if err != nil {
		return fmt.Errorf("could not record the swap, so nothing was moved; the original is untouched (%v)", err)
	}
	if err := hook("journal written"); err != nil {
		return err
	}
	undo := func(cause error) error {
		if serr := w.Journal.SetSwapState(id, jobs.SwapRolledBack); serr != nil {
			log.Printf("swap %d: %v (it is put right at the next start)", id, serr)
		}
		return cause
	}
	// 1. The original into the trash, if it is still the file that was read.
	if err := moveIntoPlace(ctx, job.Path, o.Trash, stillOK); err != nil {
		w.recordMove(job.ID, "move", job.Path, o.Trash, err)
		w.logLine(job.ID, secReplace, jobs.LevelWarning, fmt.Sprintf("Could not move the source to the kept original (trash) at %s: %v. Nothing was changed.", o.Trash, err))
		var k *KeptError
		if errors.As(err, &k) {
			return undo(err)
		}
		return undo(fmt.Errorf("move the original into %s: %w", TrashDir, err))
	}
	w.recordMove(job.ID, "move", job.Path, o.Trash, nil)
	w.logLine(job.ID, secReplace, jobs.LevelInfo, "Moved the source to the kept original (trash): "+o.Trash)
	if err := hook("original moved"); err != nil {
		return err
	}
	// 2. The new file to the original's name (or its new name: never over another file). If that
	// fails, the original goes back.
	destPath := job.Path
	if renaming {
		destPath = dest
		err = moveNoClobber(ctx, stage.path, dest, stage.intact, rules.NameTaken)
	} else {
		err = moveIntoPlace(ctx, stage.path, job.Path, stage.intact)
	}
	if err != nil {
		w.recordMove(job.ID, "move", stage.path, destPath, err)
		// No "put back" here: the put-back runs next and can fail, and the line below says it happened only when it did.
		w.logLine(job.ID, secReplace, jobs.LevelWarning, fmt.Sprintf("Could not move the new file into place at %s: %v.", destPath, err))
		putBack := os.Rename
		if renaming { // its name was free for a moment: something put there meanwhile is not replaced
			putBack = renameNoReplace
		}
		if rerr := putBack(o.Trash, job.Path); rerr != nil {
			w.recordMove(job.ID, "move", o.Trash, job.Path, rerr)
			// Leave the row "moving": the next start puts the original back.
			return fmt.Errorf("the new file could not be moved into place (%v), and putting the original back failed (%v); it is in %s and is put back at the next start", err, rerr, o.Trash)
		}
		w.recordMove(job.ID, "move", o.Trash, job.Path, nil)
		w.logLine(job.ID, secReplace, jobs.LevelInfo, "Put the source back: "+job.Path)
		var k *KeptError
		var f *FailedError
		if errors.As(err, &k) || errors.As(err, &f) {
			return undo(err)
		}
		return undo(fmt.Errorf("move result into place: %w", err))
	}
	w.recordMove(job.ID, "move", stage.path, destPath, nil)
	w.logLine(job.ID, secReplace, jobs.LevelInfo, "Moved the new file into place: "+destPath)
	if err := hook("new file in place"); err != nil {
		return err
	}
	if err := w.Journal.FinishSwap(id, w.now().AddDate(0, 0, days)); err != nil {
		// Both files are where they belong; the next start finishes the record.
		log.Printf("swap %d: %v (finished at the next start)", id, err)
		return hook("done")
	}
	if days == 0 { // kept only because the name changed: deleted now (or by the next purge)
		if isOriginal(o.Trash, o) {
			if rerr := os.Remove(o.Trash); rerr == nil {
				w.recordMove(job.ID, "remove", o.Trash, "", nil)
				w.logLine(job.ID, secReplace, jobs.LevelInfo, "Deleted the source's kept copy (trash) at "+o.Trash+": the file only changed its name.")
				w.Journal.SetSwapState(id, jobs.OriginalPurged)
				removeIfEmpty(dir)
			} else {
				w.recordMove(job.ID, "remove", o.Trash, "", rerr)
				w.logLine(job.ID, secReplace, jobs.LevelWarning, fmt.Sprintf("Could not delete the source's kept copy (trash) at %s: %v. It is deleted at the next start's cleanup.", o.Trash, rerr))
			}
		}
	}
	return hook("done")
}

// sizeRule is the "If the new file is bigger than the original" choice of Save the result (R8). It runs after the
// checks pass and before anything is moved. A new file no bigger than its original goes on as before. A bigger one
// is kept (the original stays, the job is kept with the sizes as its reason) when the flow keeps; otherwise it is
// saved anyway, and the log says so. The log line is written only for a bigger file.
func (w *Worker) sizeRule(id int64, plan rules.Plan, newPath string, origSize int64) error {
	st, err := os.Stat(newPath)
	if err != nil {
		return fmt.Errorf("the new file: %w", err)
	}
	if st.Size() <= origSize {
		return nil
	}
	w.setPhase(secReplace)
	newSize, orig := rules.HumanBytes(st.Size()), rules.HumanBytes(origSize)
	if plan.KeepIfBigger {
		w.jobf(id, ": The new file is %s, the original %s: keeping the original (this workflow keeps originals when the new file is bigger)", newSize, orig)
		// Not a failure: the workflow's own choice left the file as it is, so it ends like any other skip
		// (Skipped, left alone until it changes or Convert again), with its own reason code.
		return &SkippedError{Words: fmt.Sprintf("The new file (%s) was bigger than the original (%s), so the original was kept", newSize, orig),
			Code: jobs.ReasonNewFileBigger}
	}
	if OutputMode(plan.Output) == Replace {
		w.jobf(id, ": The new file is %s, the original %s: replacing anyway (this workflow replaces originals even when the new file is bigger)", newSize, orig)
	}
	return nil
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Completer marks a job done (the job store).
type Completer interface {
	Complete(id int64, outputPath string, sizeAfter int64) error
}

// RecoverSwaps ends every swap a crash interrupted, before any job runs (call it before
// RequeueRunning). For each row still "moving":
//   - the original is still at its name: nothing was moved; the row is rolled back and the job runs
//     again (its temp files are cleaned up as usual);
//   - the original is in the trash and the new file at its name (Dest: another name when the file type
//     changed, 25a-2): the swap is finished and the job marked done, so the new file is not converted again;
//   - the original is in the trash and nothing at Dest: the original is put back (rolled back).
//
// Anything else is left untouched and reported: the row is marked needs_attention.
// keepDays is how long a finished original is kept.
func RecoverSwaps(j Store, done Completer, keepDays int, now time.Time) []string {
	rows, err := j.Originals(jobs.SwapMoving)
	if err != nil {
		return []string{"cannot read the swap journal: " + err.Error()}
	}
	var said []string
	say := func(format string, a ...any) { said = append(said, fmt.Sprintf(format, a...)) }
	// Size, modification time (a rename on the same disk keeps it) and the sampled hash: an edit in a
	// part the samples miss still changes the time.
	isOriginal := func(p string, o jobs.Original) bool {
		st, err := os.Stat(p)
		if err != nil || st.Size() != o.Bytes || st.ModTime().UnixNano() != o.ModTime {
			return false
		}
		h, err := SampleHash(p)
		return err == nil && h == o.Hash
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	// isNew: the file at p is the checked new file the swap recorded (a regular file of its size,
	// modification time and sampled hash), not something else put there while the program was stopped.
	isNew := func(p string, o jobs.Original) bool {
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() != o.NewBytes || st.ModTime().UnixNano() != o.NewMTime {
			return false
		}
		h, err := SampleHash(p)
		return err == nil && h == o.NewHash
	}
	for _, o := range rows {
		switch {
		case isOriginal(o.Path, o):
			j.SetSwapState(o.ID, jobs.SwapRolledBack)
			say("%s: the conversion was interrupted before anything was moved; it runs again", o.Path)
		case isOriginal(o.Trash, o) && exists(o.Dest()) && !isNew(o.Dest(), o):
			j.SetSwapState(o.ID, jobs.SwapAttention)
			say("%s: a file that is not the converted one is at this name, and the original is kept in %s. Nothing was moved; please check which one you want", o.Dest(), o.Trash)
		case isOriginal(o.Trash, o) && exists(o.Dest()):
			if err := j.FinishSwap(o.ID, now.AddDate(0, 0, keepDays)); err != nil {
				say("%s: %v", o.Path, err)
				continue
			}
			size := int64(0)
			if st, err := os.Stat(o.Dest()); err == nil {
				size = st.Size()
			}
			if err := done.Complete(o.JobID, o.Dest(), size); err != nil {
				say("%s: the swap was finished, but the job could not be marked done: %v", o.Path, err)
				continue
			}
			say("%s: finished the interrupted swap; the original is kept in %s", o.Dest(), o.Trash)
		case isOriginal(o.Trash, o) && !exists(o.Dest()):
			putBack := os.Rename
			if o.NewPath != "" { // the original's own name was free meanwhile: never over a file put there
				putBack = renameNoReplace
			}
			if err := putBack(o.Trash, o.Path); err != nil {
				if errors.Is(err, fs.ErrExist) { // another file took the original's name meanwhile (25a-2)
					j.SetSwapState(o.ID, jobs.SwapAttention)
					say("%s: another file is at this name now, and the original is kept in %s. Nothing was moved; please check which one you want", o.Path, o.Trash)
					continue
				}
				say("%s: the original is in %s but could not be put back: %v", o.Path, o.Trash, err)
				continue
			}
			j.SetSwapState(o.ID, jobs.SwapRolledBack)
			say("%s: the conversion was interrupted mid-swap; the original is back in place and it runs again", o.Path)
		default:
			j.SetSwapState(o.ID, jobs.SwapAttention)
			say("%s: neither the original (expected at it or at %s) could be found as recorded; nothing was moved. Please check this file", o.Path, o.Trash)
		}
	}
	recoverRestores(j, say)
	// Swaps that were finished just before the stop, but whose job was not yet marked done.
	if n, err := j.CompleteKeptJobs(); err != nil {
		say("cannot finish the jobs of completed swaps: %v", err)
	} else if n > 0 {
		say("%d conversion(s) had finished just before the stop; they are marked done", n)
	}
	return said
}
