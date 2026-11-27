// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// ErrNotFound is a library id the catalog does not have.
var ErrNotFound = errors.New("library not found")

// ErrPathAlreadyWatched means another library already has this folder.
var ErrPathAlreadyWatched = errors.New("another library already watches this folder")

// ErrInvalidWatch means a watch value other than hourly, daily or weekly.
var ErrInvalidWatch = errors.New("watch must be hourly, daily or weekly")

// PathError refuses a folder with a sentence for the person. The cause is one of ErrMissing,
// ErrNotFolder or ErrInsideData.
type PathError struct {
	Path string
	Err  error
}

var (
	// ErrMissing is a folder that does not exist.
	ErrMissing = errors.New("folder missing")
	// ErrNotFolder is a path that exists but is not a folder.
	ErrNotFolder = errors.New("not a folder")
	// ErrInsideData is a folder inside the app's own data folder.
	ErrInsideData = errors.New("inside the data folder")
)

func (e *PathError) Error() string {
	switch {
	case errors.Is(e.Err, ErrMissing):
		return fmt.Sprintf("The folder %s does not exist. Choose the folder your videos are in.", e.Path)
	case errors.Is(e.Err, ErrNotFolder):
		return fmt.Sprintf("%s is a file, not a folder. Choose the folder your videos are in.", e.Path)
	case errors.Is(e.Err, ErrInsideData):
		return fmt.Sprintf("%s is inside the app's data folder. Choose a folder for your videos.", e.Path)
	}
	return fmt.Sprintf("%s cannot be used: %v.", e.Path, e.Err)
}

func (e *PathError) Unwrap() error { return e.Err }

// Watch is how often a library looks for new files.
type Watch string

const (
	WatchHourly Watch = "hourly"
	WatchDaily  Watch = "daily"
	WatchWeekly Watch = "weekly"
)

// ParseWatch reads a watch value; empty is daily, the page's default.
func ParseWatch(value string) (Watch, error) {
	switch Watch(strings.ToLower(strings.TrimSpace(value))) {
	case "", WatchDaily:
		return WatchDaily, nil
	case WatchHourly:
		return WatchHourly, nil
	case WatchWeekly:
		return WatchWeekly, nil
	}
	return "", fmt.Errorf("%w, not %q", ErrInvalidWatch, value)
}

// Period is one watch step.
func (w Watch) Period() time.Duration {
	switch w {
	case WatchHourly:
		return time.Hour
	case WatchWeekly:
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// State is where a catalogued file stands.
type State string

const (
	// StillChanging means the file has not settled: two looks did not agree (L-SCAN-2).
	StillChanging State = "still_changing"
	// Queued means the file was handed to Enqueue.
	Queued State = "queued"
	// Skipped means the file was settled but not queued; SkipReason says why.
	Skipped State = "skipped"
	// Gone means the file was not seen by a complete scan (L-SCAN-3).
	Gone State = "gone"
)

// Library is one folder the app watches (backend-plan §4 "libraries").
type Library struct {
	ID         int64
	Name       string
	Path       string
	ShareID    *int64
	WorkflowID *int64
	Watch      Watch
	Paused     bool
	NextScanAt *time.Time
	LastScan   *ScanSummary
}

// ScanSummary is what the last scan found; it is stored as the library's last_scan JSON and shown
// as "Last scan" on page 09.
type ScanSummary struct {
	At      time.Time `json:"at"`
	Found   int       `json:"found"`
	New     int       `json:"new"`
	Changed int       `json:"changed"`
	Gone    int       `json:"gone"`
	Queued  int       `json:"queued"`
	Skipped int       `json:"skipped"`
}

// File is one row of the catalog (backend-plan §4 "files").
type File struct {
	ID         int64
	LibraryID  int64
	RelPath    string // slash-separated, under the library's folder
	Size       int64
	MTime      time.Time
	FileID     string
	State      State
	SkipReason string
	LastJobID  *int64
	Probe      *media.Tracks
}

// NewLibrary is what Add takes. WorkflowID is nil for a library with no workflow yet; the HTTP
// layer does not send one on Add, Update sets it later.
type NewLibrary struct {
	Name       string
	Path       string
	WorkflowID *int64
	Watch      Watch
}

// Patch changes only the fields that are set. ClearWorkflow removes the workflow on its own
// (PATCH {workflowId: null}); it wins over WorkflowID when both are set.
type Patch struct {
	Name          *string
	Path          *string
	WorkflowID    *int64
	ClearWorkflow bool
	Watch         *Watch
	Paused        *bool
}

// Catalog owns the libraries and files tables.
type Catalog struct {
	db *store.DB
	// Now is the clock; tests set it. Nil means time.Now.
	Now func() time.Time
}

// NewCatalog returns a Catalog on db.
func NewCatalog(db *store.DB) *Catalog { return &Catalog{db: db} }

func (c *Catalog) clock() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// AddLibrary checks the folder, stores the library and answers it due at once: the caller starts
// its first scan (page 09 Add).
func (c *Catalog) AddLibrary(ctx context.Context, in NewLibrary) (Library, error) {
	path, err := c.checkPath(in.Path)
	if err != nil {
		return Library{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = filepath.Base(path)
	}
	watch, err := ParseWatch(string(in.Watch))
	if err != nil {
		return Library{}, err
	}
	now := c.clock().UTC().Truncate(time.Millisecond)
	if err := c.refuseWatched(ctx, path, 0); err != nil {
		return Library{}, err
	}
	var id int64
	err = c.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO libraries(name, path, workflow_id, watch, paused, next_scan_at) VALUES(?, ?, ?, ?, 0, ?)`,
			name, path, in.WorkflowID, string(watch), now.UnixMilli())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Library{}, fmt.Errorf("add the library %s: %w", path, err)
	}
	return c.GetLibrary(ctx, id)
}

// GetLibrary reads one library.
func (c *Catalog) GetLibrary(ctx context.Context, id int64) (Library, error) {
	row := c.db.Read().QueryRowContext(ctx, librarySelect+` WHERE id = ?`, id)
	lib, err := scanLibrary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return lib, err
}

// ListLibraries is every library, oldest first.
func (c *Catalog) ListLibraries(ctx context.Context) ([]Library, error) {
	rows, err := c.db.Read().QueryContext(ctx, librarySelect+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		lib, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lib)
	}
	return out, rows.Err()
}

// UpdateLibrary changes the set fields of a library and moves next_scan_at when the schedule did.
func (c *Catalog) UpdateLibrary(ctx context.Context, id int64, in Patch) (Library, error) {
	lib, err := c.GetLibrary(ctx, id)
	if err != nil {
		return Library{}, err
	}
	if in.Name != nil {
		if name := strings.TrimSpace(*in.Name); name != "" {
			lib.Name = name
		} else {
			lib.Name = filepath.Base(lib.Path)
		}
	}
	if in.Path != nil {
		path, err := c.checkPath(*in.Path)
		if err != nil {
			return Library{}, err
		}
		if err := c.refuseWatched(ctx, path, id); err != nil {
			return Library{}, err
		}
		lib.Path = path
	}
	if in.ClearWorkflow {
		lib.WorkflowID = nil
	} else if in.WorkflowID != nil {
		wid := *in.WorkflowID
		lib.WorkflowID = &wid
	}
	scheduleMoved := false
	if in.Watch != nil {
		watch, err := ParseWatch(string(*in.Watch))
		if err != nil {
			return Library{}, err
		}
		if watch != lib.Watch {
			lib.Watch = watch
			scheduleMoved = true
		}
	}
	if in.Paused != nil && *in.Paused != lib.Paused {
		lib.Paused = *in.Paused
		scheduleMoved = true
	}
	if scheduleMoved {
		lib.NextScanAt = c.arm(lib, c.clock().UTC().Truncate(time.Millisecond))
	}
	err = c.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE libraries SET name = ?, path = ?, workflow_id = ?, watch = ?, paused = ?, next_scan_at = ? WHERE id = ?`,
			lib.Name, lib.Path, lib.WorkflowID, string(lib.Watch), boolInt(lib.Paused), timePtr(lib.NextScanAt), id)
		return err
	})
	if err != nil {
		return Library{}, fmt.Errorf("save the library %d: %w", id, err)
	}
	return c.GetLibrary(ctx, id)
}

// DeleteLibrary removes the library and its catalog rows. Files on disk are not touched.
func (c *Catalog) DeleteLibrary(ctx context.Context, id int64) error {
	var n int64
	err := c.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return fmt.Errorf("remove the library %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return nil
}

// DueLibraries is the libraries whose next scan is due, earliest first. Paused libraries are never
// due (their next_scan_at is NULL).
func (c *Catalog) DueLibraries(ctx context.Context, now time.Time) ([]Library, error) {
	rows, err := c.db.Read().QueryContext(ctx,
		librarySelect+` WHERE paused = 0 AND next_scan_at IS NOT NULL AND next_scan_at <= ? ORDER BY next_scan_at`,
		now.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("read the libraries due now: %w", err)
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		lib, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lib)
	}
	return out, rows.Err()
}

// Files is a library's catalog rows, by relative path.
func (c *Catalog) Files(ctx context.Context, libraryID int64) ([]File, error) {
	rows, err := c.db.Read().QueryContext(ctx, fileSelect+` WHERE library_id = ? ORDER BY rel_path`, libraryID)
	if err != nil {
		return nil, fmt.Errorf("read the files of library %d: %w", libraryID, err)
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFileRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// checkPath turns a path into the folder's absolute cleaned form, and refuses a missing folder, a
// file or a folder inside the data folder with a sentence.
func (c *Catalog) checkPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", &PathError{Path: "The folder", Err: ErrMissing}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", &PathError{Path: path, Err: err}
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &PathError{Path: abs, Err: ErrMissing}
		}
		return "", &PathError{Path: abs, Err: err}
	}
	if !info.IsDir() {
		return "", &PathError{Path: abs, Err: ErrNotFolder}
	}
	if fsx.Inside(abs, c.db.Dir()) {
		return "", &PathError{Path: abs, Err: ErrInsideData}
	}
	return abs, nil
}

// refuseWatched stops two libraries from pointing at the same folder.
func (c *Catalog) refuseWatched(ctx context.Context, path string, exceptID int64) error {
	var id int64
	err := c.db.Read().QueryRowContext(ctx,
		`SELECT id FROM libraries WHERE path = ? AND id <> ?`, path, exceptID).Scan(&id)
	if err == nil {
		return fmt.Errorf("%w: %s", ErrPathAlreadyWatched, path)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check the library path %s: %w", path, err)
	}
	return nil
}

// arm is the next scan time: one watch period from now, on a fixed minute per library so the scans
// of many libraries do not line up (backend-plan §9). A paused library has no next scan at all.
func (c *Catalog) arm(lib Library, now time.Time) *time.Time {
	if lib.Paused {
		return nil
	}
	next := now.Add(lib.Watch.Period()).Truncate(time.Hour).Add(time.Duration(lib.ID%60) * time.Minute)
	if !next.After(now) {
		next = next.Add(time.Hour)
	}
	return &next
}

const librarySelect = `SELECT id, name, path, share_id, workflow_id, watch, paused, next_scan_at, last_scan FROM libraries`

const fileSelect = `SELECT id, library_id, rel_path, size, mtime, file_id, probe, state, skip_reason, last_job_id FROM files`

type scanner interface {
	Scan(dest ...any) error
}

func scanLibrary(row scanner) (Library, error) {
	var (
		lib     Library
		watch   string
		paused  int
		next    sql.NullInt64
		lastRaw sql.NullString
	)
	if err := row.Scan(&lib.ID, &lib.Name, &lib.Path, &lib.ShareID, &lib.WorkflowID, &watch, &paused, &next, &lastRaw); err != nil {
		return Library{}, err
	}
	lib.Watch = Watch(watch)
	lib.Paused = paused != 0
	if next.Valid {
		t := time.UnixMilli(next.Int64).UTC()
		lib.NextScanAt = &t
	}
	if lastRaw.Valid && lastRaw.String != "" {
		var summary ScanSummary
		if err := json.Unmarshal([]byte(lastRaw.String), &summary); err != nil {
			return Library{}, fmt.Errorf("read the last scan of library %d: %w", lib.ID, err)
		}
		lib.LastScan = &summary
	}
	return lib, nil
}

func scanFileRow(row scanner) (File, error) {
	var (
		f          File
		mtime      int64
		probeRaw   sql.NullString
		state      string
		lastJobRaw sql.NullInt64
	)
	if err := row.Scan(&f.ID, &f.LibraryID, &f.RelPath, &f.Size, &mtime, &f.FileID, &probeRaw,
		&state, &f.SkipReason, &lastJobRaw); err != nil {
		return File{}, err
	}
	f.MTime = time.Unix(0, mtime).UTC()
	f.State = State(state)
	if probeRaw.Valid && probeRaw.String != "" {
		var tracks media.Tracks
		if err := json.Unmarshal([]byte(probeRaw.String), &tracks); err != nil {
			return File{}, fmt.Errorf("read the probe of file %d: %w", f.ID, err)
		}
		f.Probe = &tracks
	}
	if lastJobRaw.Valid {
		id := lastJobRaw.Int64
		f.LastJobID = &id
	}
	return f, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func timePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().UnixMilli()
}
