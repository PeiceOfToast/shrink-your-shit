// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// againStore is what Convert again needs from the job store (design 11:45Z), beyond Queue.
type againStore interface {
	AgainFrom(id int64) int64
	OriginalByID(id int64) (jobs.Original, error)
	Reconverted(id int64, newPath string, newBytes, newMTime int64, newHash string, deleteOn time.Time) error
}

// againSource is the file job converts from: its original kept in .syp-trash when it is converted again
// after replacing it (with that original's record), else the file at its name.
func (w *Worker) againSource(job jobs.Job) (string, *jobs.Original, error) {
	as, ok := w.Queue.(againStore)
	if !ok {
		return job.Path, nil, nil
	}
	id := as.AgainFrom(job.ID)
	if id == 0 {
		return job.Path, nil, nil
	}
	o, err := as.OriginalByID(id)
	if err != nil || o.State != jobs.OriginalKept || !isOriginal(o.Trash, o) {
		return "", nil, failed("Can't convert again: the original kept in "+TrashDir+" is no longer there as it was kept. Nothing was changed.", errors.New(jobs.AgainOriginalGone))
	}
	return o.Trash, &o, nil
}

// againLeftAlone: the workflow as it is now leaves the original alone, so a Convert again changes nothing.
func againLeftAlone() error {
	return failed("The workflow as it is now leaves this file alone, so it was not converted again. Your file and its kept original were not touched.", errors.New("convert again: left alone"))
}

// replaceOwnOutput puts the checked new file of a Convert again at final, in place of our earlier file
// (o.Dest()), which must still be the one recorded at the first swap: one rename over it, or, when the
// name changes, a rename that replaces nothing and then our earlier file goes. The original stays kept
// in .syp-trash, its days starting again now (design 11:45Z).
func (w *Worker) replaceOwnOutput(ctx context.Context, o jobs.Original, stage *staged, final string, days int, stillOK func() error) error {
	old := o.Dest()
	notOurs := func() error {
		if _, err := os.Lstat(old); err == nil && !isConvertedFile(old, o) {
			return failed(fmt.Sprintf("The file at %s is not the one Shrink Your Shit made from this original, so it was not replaced. Nothing was changed. Move or rename it, then press Convert again.", old), fs.ErrExist)
		}
		return nil
	}
	ours := func() error { // right before the rename: the original, the new file, and our earlier file as recorded
		if err := stillOK(); err != nil {
			return err
		}
		return notOurs()
	}
	if err := notOurs(); err != nil {
		return err
	}
	var err error
	if final == old {
		err = moveIntoPlace(ctx, stage.path, final, ours)
	} else {
		err = moveNoClobber(ctx, stage.path, final, ours, rules.NameTaken)
		if err == nil && isConvertedFile(old, o) {
			if rerr := os.Remove(old); rerr != nil {
				log.Printf("convert again: our earlier file %s: %v", old, rerr)
			}
		}
	}
	if err != nil {
		var f *FailedError
		var k *KeptError
		if errors.As(err, &f) || errors.As(err, &k) {
			return err
		}
		return retryable(failed("The new file could not be put in place, so your file was kept as it was. Press Retry.", fmt.Errorf("convert again: %w", err)), retryGeneral)
	}
	st, serr := os.Stat(final)
	h, herr := SampleHash(final)
	newPath := ""
	if final != o.Path {
		newPath = final
	}
	if as, ok := w.Queue.(againStore); ok && serr == nil && herr == nil {
		if err := as.Reconverted(o.ID, newPath, st.Size(), st.ModTime().UnixNano(), h, w.now().AddDate(0, 0, days)); err != nil {
			log.Printf("convert again: the record of %s: %v", o.Trash, err)
		}
	}
	return nil
}
