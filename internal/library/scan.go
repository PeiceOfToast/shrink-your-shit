// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// ErrAlreadyQueued is what Enqueue answers when the file already has a waiting or running job
// (L-SCAN-5). The scanner counts it as queued, not as a failure.
var ErrAlreadyQueued = errors.New("the file already has an open job")

// DefaultSettle is L-SCAN-2's default wait between the two looks. One scan looks, waits, and looks
// again; a file that changed in between is still_changing.
const DefaultSettle = 15 * time.Second

// DefaultProgressEvery is how many settled files pass between scan.progress events.
const DefaultProgressEvery = 500

// The reasons a settled file was not queued. They are stored on the row and are the strings the
// Waiting page will group by.
const (
	skipSharedFile = "shared_file"
	skipOutput     = "output_folder"
	skipNotMedia   = "not_media"
	skipUnreadable = "unreadable"
	skipEnqueue    = "enqueue_failed"
	skipVanished   = "vanished"
)

// Emitter is the event bus as a scan uses it. Bus.Emit has this shape.
type Emitter interface {
	Emit(events.Event) error
}

// Scanner walks one library's folder and keeps the catalog up to date (backend-plan §9). Prober,
// Decide and Enqueue are injected: the media package probes, and item 9 wires the workflow's decide
// and jobs.Enqueue without this package importing either.
type Scanner struct {
	Catalog *Catalog
	// Bus receives scan.start, scan.progress and scan.end. Nil sends nothing.
	Bus Emitter
	// Prober asks ffprobe about a settled file.
	Prober media.Prober
	// Decide says whether a settled, probed file should be queued; skipReason is stored when not.
	Decide func(File, media.Info) (queue bool, skipReason string)
	// Enqueue hands a file to the job queue. Returning ErrAlreadyQueued counts as queued.
	Enqueue func(ctx context.Context, lib Library, file File) error
	// Now and Sleep are the scan's clock and its settle wait; tests set them. Nil means time.Now
	// and time.Sleep.
	Now   func() time.Time
	Sleep func(time.Duration)
	// Settle is how long the two looks are apart. Zero means DefaultSettle.
	Settle time.Duration
	// ProgressEvery is the file step between scan.progress events. Zero means DefaultProgressEvery;
	// a negative value sends none.
	ProgressEvery int
	// OutputDirs names the folders the app writes converted files into. A file under one of them is
	// never catalogued, even when the folder sits inside the library (L-SCAN-1).
	OutputDirs func(Library) []string

	mu      sync.Mutex
	running map[int64]*scanCall
}

// ScanResult is what one scan found. SkippedBy counts the reasons, so the caller can see a guard at
// work rather than only a total.
type ScanResult struct {
	Found         int
	New           int
	Changed       int
	Gone          int
	Queued        int
	Skipped       int
	StillChanging int
	SkippedBy     map[string]int
}

type scanCall struct {
	done   chan struct{}
	result ScanResult
	err    error
}

// scanFile is one file as the walk saw it. refuse is why a guard leaves the file alone; such a
// file still counts as found, so its row is not marked gone, but it is never settled or queued.
type scanFile struct {
	rel    string
	abs    string
	size   int64
	mtime  time.Time
	fileID string
	refuse string
}

// Scan brings one library's catalog up to date. One scan per library runs at a time; a Scan call
// while one runs waits for it and returns the same result (backend-plan §9).
func (s *Scanner) Scan(ctx context.Context, lib Library) (ScanResult, error) {
	s.mu.Lock()
	if s.running == nil {
		s.running = make(map[int64]*scanCall)
	}
	if call, ok := s.running[lib.ID]; ok {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.result, call.err
		case <-ctx.Done():
			return ScanResult{}, ctx.Err()
		}
	}
	call := &scanCall{done: make(chan struct{})}
	s.running[lib.ID] = call
	s.mu.Unlock()

	call.result, call.err = s.scan(ctx, lib)

	s.mu.Lock()
	delete(s.running, lib.ID)
	s.mu.Unlock()
	close(call.done)
	return call.result, call.err
}

func (s *Scanner) scan(ctx context.Context, lib Library) (ScanResult, error) {
	res := ScanResult{SkippedBy: map[string]int{}}
	if s.Catalog == nil || s.Prober == nil || s.Decide == nil || s.Enqueue == nil {
		return res, errors.New("scan: the scanner needs a catalog, a prober, a Decide and an Enqueue")
	}
	if info, err := os.Stat(lib.Path); err != nil {
		s.event(events.Event{Level: events.LevelError, Kind: events.KindError, Message: fmt.Sprintf("Cannot scan %s: %v", lib.Path, err)})
		return res, fmt.Errorf("scan %s: %w", lib.Path, err)
	} else if !info.IsDir() {
		return res, fmt.Errorf("scan %s: %w", lib.Path, ErrNotFolder)
	}

	// Resolve the root once and walk the folder it points at: a library path that is itself a
	// link (a NAS mount alias, a folder alias) must scan what the link names. WalkDir does not
	// follow a root symlink, and an unresolved link would leave the walk empty and look complete.
	// Every relative path, the settle stat and the guards then share one real root.
	root, err := filepath.EvalSymlinks(filepath.Clean(lib.Path))
	if err != nil {
		s.event(events.Event{Level: events.LevelError, Kind: events.KindError, Message: fmt.Sprintf("Cannot scan %s: %v", lib.Path, err)})
		return res, fmt.Errorf("scan %s: %w", lib.Path, err)
	}

	now := s.now()
	s.event(events.Event{
		Level: events.LevelInfo, Kind: events.KindScanStart, Message: "Scanning " + lib.Name,
		LibraryID: strconv.FormatInt(lib.ID, 10),
	})

	seen, complete, err := s.walk(ctx, lib, root, &res)
	if err != nil {
		return res, err
	}
	res.Found = len(seen)

	rows, err := s.Catalog.Files(ctx, lib.ID)
	if err != nil {
		return res, err
	}
	byPath := make(map[string]File, len(rows))
	byID := make(map[string]File, len(rows))
	for _, row := range rows {
		byPath[row.RelPath] = row
		if row.FileID != "" {
			byID[row.FileID] = row
		}
	}
	renamed := make(map[int64]bool) // catalog rows whose path a rename moved

	var candidates []scanFile
	for _, f := range seen {
		if f.refuse != "" {
			// A guard refused it: record the reason on its row, follow a rename, and stop there.
			// A file the walk could not stat has no new facts to store and keeps its old row.
			if moved, ok := byID[f.fileID]; ok && f.fileID != "" && moved.RelPath != f.rel {
				renamed[moved.ID] = true
				if err := s.renameRow(ctx, moved.ID, f.rel); err != nil {
					return res, err
				}
			}
			if f.abs != "" {
				out := File{LibraryID: lib.ID, RelPath: f.rel, Size: f.size, MTime: f.mtime, FileID: f.fileID}
				if err := s.saveFile(ctx, out, Skipped, f.refuse, nil); err != nil {
					return res, err
				}
			}
			continue
		}
		row, ok := byPath[f.rel]
		if !ok {
			// Settle follows the file, not the path (L-SCAN-2): a renamed file keeps its row and
			// its state instead of restarting as new.
			if moved, ok := byID[f.fileID]; ok && f.fileID != "" {
				renamed[moved.ID] = true
				if err := s.renameRow(ctx, moved.ID, f.rel); err != nil {
					return res, err
				}
				if moved.Size == f.size && moved.MTime.Equal(f.mtime) {
					continue
				}
				res.Changed++
				candidates = append(candidates, f)
				continue
			}
			res.New++
			candidates = append(candidates, f)
			continue
		}
		moved := row.State == Gone || row.Size != f.size || !row.MTime.Equal(f.mtime) || row.FileID != f.fileID
		// A still_changing row is taken to the second look again even when nothing moved: its state
		// is the promise that the next scan checks whether it settled (L-SCAN-2). Only a file whose
		// facts moved is counted as changed.
		if moved || row.State == StillChanging {
			if moved {
				res.Changed++
			}
			candidates = append(candidates, f)
		}
	}

	if len(candidates) > 0 {
		settled, err := s.settle(ctx, lib, root, candidates, &res)
		if err != nil {
			return res, err
		}
		if err := s.decide(ctx, lib, settled, &res); err != nil {
			return res, err
		}
	}

	if complete {
		seenSet := make(map[string]struct{}, len(seen))
		for _, f := range seen {
			seenSet[f.rel] = struct{}{}
		}
		for _, row := range rows {
			if _, ok := seenSet[row.RelPath]; ok || row.State == Gone || renamed[row.ID] {
				continue
			}
			if err := s.Catalog.db.Tx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE files SET state = 'gone' WHERE id = ?`, row.ID)
				return err
			}); err != nil {
				return res, fmt.Errorf("mark %s gone: %w", row.RelPath, err)
			}
			res.Gone++
		}
	}

	summary := ScanSummary{
		At: now, Found: res.Found, New: res.New, Changed: res.Changed,
		Gone: res.Gone, Queued: res.Queued, Skipped: res.Skipped,
	}
	if err := s.saveScan(ctx, lib, summary, now); err != nil {
		return res, err
	}
	s.event(events.Event{
		Level: events.LevelInfo, Kind: events.KindScanEnd, Message: scanMessage(summary),
		LibraryID: strconv.FormatInt(lib.ID, 10), DurationMS: s.now().Sub(now).Milliseconds(),
		Data: map[string]any{
			"found": res.Found, "new": res.New, "changed": res.Changed, "gone": res.Gone,
			"queued": res.Queued, "skipped": res.Skipped, "stillChanging": res.StillChanging,
			"skippedBy": res.SkippedBy,
		},
	})
	return res, nil
}

// walk lists the files the guards allow. seen holds every regular file the walk met, including the
// ones a file guard refuses, so a refused file with a catalog row is not marked gone. complete is
// false when a folder could not be read, and when the walk never entered the root as a folder:
// such a scan marks nothing gone (L-SCAN-3).
func (s *Scanner) walk(ctx context.Context, lib Library, root string, res *ScanResult) (seen []scanFile, complete bool, err error) {
	complete = true
	outputs := s.outputDirs(lib)
	rootDir := false

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if walkErr != nil {
			// An unreadable subfolder keeps its files' state; the scan goes on (L-SCAN-3).
			if path == root {
				return walkErr
			}
			complete = false
			s.event(events.Event{
				Level: events.LevelWarn, Kind: events.KindWarning, Message: fmt.Sprintf("Cannot read %s: %v", path, walkErr),
				LibraryID: strconv.FormatInt(lib.ID, 10),
			})
			return nil
		}
		if path == root {
			// Belt and braces behind the resolved root: an empty walk that never stood in the
			// folder is not evidence that the files are gone. The root itself is never skipped
			// or guarded.
			rootDir = d.IsDir()
			return nil
		}
		if d.IsDir() {
			if d.Type()&fs.ModeSymlink != 0 || skipName(d.Name()) || insideAny(path, outputs) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // a symlink is never even opened (L-SCAN-1)
		}
		if insideAny(path, outputs) {
			res.skip(skipOutput) // our own output folder inside the library
			return nil
		}
		if skipName(d.Name()) {
			return nil // our temp and staging names, and hidden files
		}
		f, err := s.stat(path, root)
		if err != nil {
			res.skip(skipUnreadable)
			s.event(events.Event{
				Level: events.LevelWarn, Kind: events.KindWarning, Message: fmt.Sprintf("Cannot read %s: %v", path, err),
				LibraryID: strconv.FormatInt(lib.ID, 10),
			})
			seen = append(seen, scanFile{rel: relPath(root, path), refuse: skipUnreadable})
			return nil
		}
		links, err := fsx.LinkCount(path)
		if err == nil && links > 1 {
			// More than one name points at this file. Windows tells the same from the file's link
			// count (ByHandleFileInformation.NumberOfLinks); we cannot tell where the other name is,
			// so the file is left alone (L-SCAN-1).
			res.skip(skipSharedFile)
			f.refuse = skipSharedFile
			seen = append(seen, f)
			return nil
		}
		seen = append(seen, f)
		return nil
	})
	if walkErr != nil {
		return nil, false, fmt.Errorf("walk %s: %w", lib.Path, walkErr)
	}
	if !rootDir {
		complete = false
		s.event(events.Event{
			Level: events.LevelWarn, Kind: events.KindWarning,
			Message:   fmt.Sprintf("Cannot scan %s: it was not walked as a folder", root),
			LibraryID: strconv.FormatInt(lib.ID, 10),
		})
	}
	return seen, complete, nil
}

// settle takes the second look: after one wait for all candidates, a file is settled only when the
// size and mtime are the same as the first look (L-SCAN-2). Empty files never settle.
func (s *Scanner) settle(ctx context.Context, lib Library, root string, candidates []scanFile, res *ScanResult) ([]scanFile, error) {
	s.sleep(s.settleWait())

	var settled []scanFile
	for _, f := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		second, err := s.stat(f.abs, root)
		if err != nil {
			res.StillChanging++
			if err := s.saveStillChanging(ctx, lib, f, skipUnreadable); err != nil {
				return nil, err
			}
			continue
		}
		if second.size == 0 || second.size != f.size || !second.mtime.Equal(f.mtime) {
			res.StillChanging++
			if err := s.saveStillChanging(ctx, lib, second, ""); err != nil {
				return nil, err
			}
			continue
		}
		settled = append(settled, second)
	}
	return settled, nil
}

// decide probes each settled file, asks the injected Decide, and enqueues or skips it. A file the
// prober or the enqueue refuses is a skip with a reason, not a failed scan.
func (s *Scanner) decide(ctx context.Context, lib Library, files []scanFile, res *ScanResult) error {
	every := s.ProgressEvery
	if every == 0 {
		every = DefaultProgressEvery
	}
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		out := File{LibraryID: lib.ID, RelPath: f.rel, Size: f.size, MTime: f.mtime, FileID: f.fileID}

		info, err := s.Prober.Probe(ctx, f.abs)
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			reason := skipUnreadable
			switch {
			case errors.Is(err, media.ErrNotMedia):
				reason = skipNotMedia
			case errors.Is(err, fs.ErrNotExist):
				// It left between the walk and the probe: not there is not a problem to show
				// anyone. The next complete scan marks its row gone.
				reason = skipVanished
			}
			res.skip(reason)
			if err := s.saveFile(ctx, out, Skipped, reason, nil); err != nil {
				return err
			}
			if reason != skipVanished {
				s.event(events.Event{
					Level: events.LevelWarn, Kind: events.KindWarning, Message: fmt.Sprintf("Skipped %s: %v", f.rel, err),
					LibraryID: strconv.FormatInt(lib.ID, 10),
				})
			}
			continue
		}

		tracks := media.Summarize(info)
		out.Probe = &tracks
		queue, reason := s.Decide(out, info)
		if !queue {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				reason = "skipped"
			}
			res.skip(reason)
			if err := s.saveFile(ctx, out, Skipped, reason, &tracks); err != nil {
				return err
			}
		} else if err := s.Enqueue(ctx, lib, out); err != nil && !errors.Is(err, ErrAlreadyQueued) {
			res.skip(skipEnqueue)
			if err := s.saveFile(ctx, out, Skipped, skipEnqueue, &tracks); err != nil {
				return err
			}
			s.event(events.Event{
				Level: events.LevelWarn, Kind: events.KindWarning, Message: fmt.Sprintf("Could not queue %s: %v", f.rel, err),
				LibraryID: strconv.FormatInt(lib.ID, 10),
			})
		} else {
			res.Queued++ // ErrAlreadyQueued is a success: L-SCAN-5 allows one open job per file
			if err := s.saveFile(ctx, out, Queued, "", &tracks); err != nil {
				return err
			}
		}

		if every > 0 && (i+1)%every == 0 {
			s.event(events.Event{
				Level: events.LevelInfo, Kind: events.KindScanProgress,
				Message:   fmt.Sprintf("Looking at files: %d of %d", i+1, len(files)),
				LibraryID: strconv.FormatInt(lib.ID, 10),
				Data:      map[string]any{"done": i + 1, "total": len(files)},
			})
		}
	}
	return nil
}

// QueueFiles runs Decide over the library's counted files and enqueues the ones that should convert,
// with no rescan (page 09: giving a needs_workflow library its first workflow starts queueing at
// once). A file the last scan saw mid-copy is left for the next scan: this path settles nothing, so
// it must not queue a file that may still be growing. A row whose file has moved since the count is
// left the same way — the next scan settles it. Each file is probed from disk again rather than read
// from its stored summary: the summary is not the whole probe the workflow's Plan reads. Nothing is
// walked or settled, no scan events are sent, and the scan's schedule is left alone. It answers how
// many files it queued, counting one that already has an open job as queued, as a scan does.
func (s *Scanner) QueueFiles(ctx context.Context, lib Library) (int, error) {
	if s.Catalog == nil || s.Prober == nil || s.Decide == nil || s.Enqueue == nil {
		return 0, errors.New("queue files: the scanner needs a catalog, a prober, a Decide and an Enqueue")
	}
	rows, err := s.Catalog.Files(ctx, lib.ID)
	if err != nil {
		return 0, err
	}
	root := filepath.Clean(lib.Path)
	files := make([]scanFile, 0, len(rows))
	for _, row := range rows {
		if row.State == Gone || row.State == StillChanging {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(row.RelPath))
		info, err := os.Stat(abs)
		if err != nil {
			// Gone or unreadable now: the next scan records what happened to its row.
			continue
		}
		if info.Size() != row.Size || !info.ModTime().Equal(row.MTime) {
			// It moved since the count, and this path does not settle: the next scan does.
			continue
		}
		files = append(files, scanFile{
			rel: row.RelPath, abs: abs, size: row.Size, mtime: row.MTime, fileID: row.FileID,
		})
	}
	res := ScanResult{SkippedBy: map[string]int{}}
	// The caller has just saved the workflow: Decide reads the library's workflow from the catalog.
	if err := s.decide(ctx, lib, files, &res); err != nil {
		return res.Queued, err
	}
	return res.Queued, nil
}

// renameRow points an existing catalog row at the file's new relative path.
func (s *Scanner) renameRow(ctx context.Context, id int64, rel string) error {
	err := s.Catalog.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE files SET rel_path = ? WHERE id = ?`, rel, id)
		return err
	})
	if err != nil {
		return fmt.Errorf("follow the rename to %s: %w", rel, err)
	}
	return nil
}

// saveStillChanging records a file whose two looks did not agree.
func (s *Scanner) saveStillChanging(ctx context.Context, lib Library, f scanFile, reason string) error {
	out := File{LibraryID: lib.ID, RelPath: f.rel, Size: f.size, MTime: f.mtime, FileID: f.fileID}
	return s.saveFile(ctx, out, StillChanging, reason, nil)
}

// saveFile upserts one catalog row through the catalog, which the job wiring also calls to get a
// file's id before it enqueues it.
func (s *Scanner) saveFile(ctx context.Context, f File, state State, reason string, probe *media.Tracks) error {
	_, err := s.Catalog.SaveFile(ctx, f, state, reason, probe)
	return err
}

// saveScan stores the counts and arms the next scan unless the library is paused. A library removed
// while its scan ran is simply not updated.
func (s *Scanner) saveScan(ctx context.Context, lib Library, summary ScanSummary, now time.Time) error {
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	current, err := s.Catalog.GetLibrary(ctx, lib.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	next := s.Catalog.arm(current, now)
	err = s.Catalog.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE libraries SET last_scan = ?, next_scan_at = ? WHERE id = ?`,
			string(data), timePtr(next), lib.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("save the scan of library %d: %w", lib.ID, err)
	}
	return nil
}

// stat reads one file's size, mtime and file id. A renamed file keeps its id, so the catalog
// follows the file rather than the path (L-SCAN-2).
func (s *Scanner) stat(path, root string) (scanFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return scanFile{}, err
	}
	id, err := fsx.FileID(path)
	if err != nil {
		return scanFile{}, err
	}
	return scanFile{
		rel: relPath(root, path), abs: path,
		size: info.Size(), mtime: info.ModTime(), fileID: id,
	}, nil
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	return filepath.ToSlash(rel)
}

// skipName is the folder-and-file name guard: hidden names, our temporary names, and the system
// and recycle folders (L-SCAN-1). Comparison is case-insensitive so a Windows recycle folder is
// caught whatever its case.
func skipName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".syp-tmp") {
		return true
	}
	switch lower {
	case "@eadir", "$recycle.bin", "system volume information", "lost+found",
		"#recycle", "#snapshot", "@recycle":
		return true
	}
	return false
}

// insideAny reports whether path sits in one of the folders we write outputs into.
func insideAny(path string, folders []string) bool {
	for _, folder := range folders {
		if fsx.Inside(path, folder) {
			return true
		}
	}
	return false
}

func (r *ScanResult) skip(reason string) {
	r.Skipped++
	r.SkippedBy[reason]++
}

func (s *Scanner) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (s *Scanner) settleWait() time.Duration {
	if s.Settle > 0 {
		return s.Settle
	}
	return DefaultSettle
}

func (s *Scanner) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scanner) outputDirs(lib Library) []string {
	if s.OutputDirs == nil {
		return nil
	}
	return s.OutputDirs(lib)
}

func (s *Scanner) event(e events.Event) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Emit(e)
}

func scanMessage(summary ScanSummary) string {
	return fmt.Sprintf("Scanned: %d found, %d new, %d changed, %d gone, %d queued, %d skipped",
		summary.Found, summary.New, summary.Changed, summary.Gone, summary.Queued, summary.Skipped)
}
