// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library is a folder the program looks after, with exactly one flow saying what to do with its
// files. A new library is not started: nothing is scanned or queued until it is (StartLibrary).

// Library is one library record.
type Library struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	FlowID  int64  `json:"flowId"`
	Started bool   `json:"started"`
	Created string `json:"createdAt"`
}

// Flow is one flow record. Its behaviour is its template until the graph engine (slice 30).
type Flow struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	Template rules.Template `json:"template"`
	Created  string         `json:"createdAt"`
	Changed  string         `json:"changedAt"`
}

// ErrNotFound: no such record.
var ErrNotFound = errors.New("not found")

// ErrPathTaken: another library already has this folder.
var ErrPathTaken = errors.New("another library already uses this folder")

// ErrLibraryGone: a job was not added because its library was removed or is not started.
var ErrLibraryGone = errors.New("its library was removed or is not started")

// RealPath is a library folder as stored: absolute, cleaned and with links resolved, so the scanner
// (which does not follow a link at the top of a folder tree) walks the real folder, and two names
// for one folder are seen as one.
func RealPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// Within reports whether p is inside dir (not equal to it).
func Within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

const libCols = `id,name,path,flow_id,started,created`

func scanLibrary(r interface{ Scan(...any) error }) (Library, error) {
	var l Library
	err := r.Scan(&l.ID, &l.Name, &l.Path, &l.FlowID, &l.Started, &l.Created)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return l, err
}

// Libraries lists every library, oldest first.
func (s *Store) Libraries() ([]Library, error) {
	rows, err := s.db.Query(`SELECT ` + libCols + ` FROM libraries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		l, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetLibrary returns one library (ErrNotFound if there is none).
func (s *Store) GetLibrary(id int64) (Library, error) {
	return scanLibrary(s.db.QueryRow(`SELECT `+libCols+` FROM libraries WHERE id=?`, id))
}

// GetFlow returns one flow (ErrNotFound if there is none).
func (s *Store) GetFlow(id int64) (Flow, error) {
	var f Flow
	var t string
	err := s.db.QueryRow(`SELECT id,name,template,created,changed FROM flows WHERE id=?`, id).
		Scan(&f.ID, &f.Name, &t, &f.Created, &f.Changed)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal([]byte(t), &f.Template)
}

type execer interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

// uniqueFlowName is base, or base plus " 2", " 3"... if a flow already has that name.
func uniqueFlowName(q execer, base string) (string, error) {
	name := base
	for n := 2; ; n++ {
		var c int
		if err := q.QueryRow(`SELECT COUNT(*) FROM flows WHERE name=?`, name).Scan(&c); err != nil {
			return "", err
		}
		if c == 0 {
			return name, nil
		}
		name = fmt.Sprintf("%s %d", base, n)
	}
}

func (s *Store) addFlow(q execer, name string, t rules.Template) (int64, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return 0, err
	}
	now := s.stamp()
	r, err := q.Exec(`INSERT INTO flows(name,template,created,changed) VALUES(?,?,?,?)`, name, string(b), now, now)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) pathTaken(q execer, path string, except int64) error {
	var c int
	if err := q.QueryRow(`SELECT COUNT(*) FROM libraries WHERE path=? AND id<>?`, path, except).Scan(&c); err != nil {
		return err
	}
	if c > 0 {
		return ErrPathTaken
	}
	return nil
}

// AddLibrary adds a library with its own new flow made from t. The flow is named after the library
// (a clash adds " 2", " 3"...), once; it is never renamed automatically afterwards.
func (s *Store) AddLibrary(name, path string, t rules.Template, started bool) (Library, error) {
	return s.addLibrary(name, path, name, t, started)
}

func (s *Store) addLibrary(name, path, flowBase string, t rules.Template, started bool) (Library, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	if err := s.pathTaken(tx, path, 0); err != nil {
		return Library{}, err
	}
	flowName, err := uniqueFlowName(tx, flowBase)
	if err != nil {
		return Library{}, err
	}
	flowID, err := s.addFlow(tx, flowName, t)
	if err != nil {
		return Library{}, err
	}
	r, err := tx.Exec(`INSERT INTO libraries(name,path,flow_id,started,created) VALUES(?,?,?,?,?)`,
		name, path, flowID, started, s.stamp())
	if err != nil {
		return Library{}, err
	}
	id, _ := r.LastInsertId()
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	return s.GetLibrary(id)
}

// Upgraded reports whether this database held work before libraries existed (an upgrade), as
// opposed to a fresh install. Decided once, by migration 3.
func (s *Store) Upgraded() (bool, error) {
	v, _, err := s.Meta("install")
	return v == "upgrade", err
}

// EnsureLibraries adds a library for each folder in paths that no library has yet. It never changes
// or removes a library. On an upgrade, each is started with its own flow made from legacy (the
// program's flags), so it converts as before; on the first start after the upgrade those flows are
// named "<folder> - Legacy". On a fresh install each is NOT started and gets a slider flow named after
// it: first-run shows it and nothing is converted before Start (DESIGN/FIRST-RUN.md). A folder inside,
// or around, another library's folder is not added: skipped says why, for a log line (their files
// would belong to two libraries). Jobs made before libraries were records are linked to the library
// whose folder holds them.
func (s *Store) EnsureLibraries(paths []string, legacy rules.Template) (added int, skipped []string, err error) {
	upgraded, err := s.Upgraded()
	if err != nil {
		return 0, nil, err
	}
	var marker int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM app_meta WHERE key='legacy_flows'`).Scan(&marker); err != nil {
		return 0, nil, err
	}
	for _, given := range paths {
		p, err := RealPath(given)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", given, err))
			continue
		}
		libs, err := s.Libraries()
		if err != nil {
			return added, skipped, err
		}
		have, clash := false, ""
		for _, l := range libs {
			switch {
			case l.Path == p || l.Path == given:
				have = true
			case Within(p, l.Path) || Within(l.Path, p):
				clash = l.Path
			}
		}
		if have {
			continue
		}
		if clash != "" {
			skipped = append(skipped, fmt.Sprintf("%s: it is inside, or contains, the folder of another library (%s)", given, clash))
			continue
		}
		name := filepath.Base(p)
		flowBase, t, started := name, rules.SliderTemplate(rules.DefaultSliderPosition), false
		if upgraded {
			t, started = legacy, true
			if marker > 0 {
				flowBase = name + " - Legacy"
			}
		}
		if _, err := s.addLibrary(name, p, flowBase, t, started); err != nil {
			return added, skipped, err
		}
		added++
	}
	if marker > 0 {
		if _, err := s.db.Exec(`DELETE FROM app_meta WHERE key='legacy_flows'`); err != nil {
			return added, skipped, err
		}
	}
	return added, skipped, s.linkOldJobs()
}

// linkOldJobs gives jobs made before libraries were records the library whose folder holds the file.
func (s *Store) linkOldJobs() error {
	rows, err := s.db.Query(`SELECT id, path FROM jobs WHERE library_id IS NULL`)
	if err != nil {
		return err
	}
	type old struct {
		id   int64
		path string
	}
	var olds []old
	for rows.Next() {
		var o old
		rows.Scan(&o.id, &o.path)
		olds = append(olds, o)
	}
	rows.Close()
	if len(olds) == 0 {
		return nil
	}
	libs, err := s.Libraries()
	if err != nil {
		return err
	}
	for _, o := range olds {
		real := o.path
		if r, err := RealPath(filepath.Dir(o.path)); err == nil {
			real = filepath.Join(r, filepath.Base(o.path))
		}
		for _, l := range libs {
			if Within(o.path, l.Path) || Within(real, l.Path) {
				if _, err := s.db.Exec(`UPDATE jobs SET library_id=? WHERE id=?`, l.ID, o.id); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// UpdateLibrary changes a library's name and/or folder (nil leaves it as it is).
func (s *Store) UpdateLibrary(id int64, name, path *string) (Library, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	if _, err := scanLibrary(tx.QueryRow(`SELECT `+libCols+` FROM libraries WHERE id=?`, id)); err != nil {
		return Library{}, err
	}
	if name != nil {
		if _, err := tx.Exec(`UPDATE libraries SET name=? WHERE id=?`, *name, id); err != nil {
			return Library{}, err
		}
	}
	if path != nil {
		if err := s.pathTaken(tx, *path, id); err != nil {
			return Library{}, err
		}
		if _, err := tx.Exec(`UPDATE libraries SET path=? WHERE id=?`, *path, id); err != nil {
			return Library{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	return s.GetLibrary(id)
}

// StartLibrary marks a library started: from now on it is scanned.
func (s *Store) StartLibrary(id int64) error {
	r, err := s.db.Exec(`UPDATE libraries SET started=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteLibrary removes the library record and its queued jobs. Media files, its flow and its
// finished jobs stay. It returns the library's running job, if any, which the caller must stop
// (the worker then drops it; see Drop).
func (s *Store) DeleteLibrary(id int64) (runningJob int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`DELETE FROM libraries WHERE id=?`, id)
	if err != nil {
		return 0, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM jobs WHERE library_id=? AND status=?`, id, Queued); err != nil {
		return 0, err
	}
	err = tx.QueryRow(`SELECT id FROM jobs WHERE library_id=? AND status=? LIMIT 1`, id, Running).Scan(&runningJob)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return 0, err
	}
	return runningJob, tx.Commit()
}

// Drop deletes a job record that was stopped because its library was removed.
func (s *Store) Drop(id int64) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE id=?`, id)
	return err
}

// SettingsFor returns what the job's library's flow says to do. A job made before libraries were
// records is matched to its library by folder.
func (s *Store) SettingsFor(j Job) (rules.Settings, error) {
	var flowID int64
	var err error
	if j.LibraryID > 0 {
		err = s.db.QueryRow(`SELECT flow_id FROM libraries WHERE id=?`, j.LibraryID).Scan(&flowID)
	} else {
		err = s.db.QueryRow(`SELECT flow_id FROM libraries WHERE path=?`, j.Library).Scan(&flowID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return rules.Settings{}, errors.New("its library no longer exists")
	}
	if err != nil {
		return rules.Settings{}, err
	}
	f, err := s.GetFlow(flowID)
	if err != nil {
		return rules.Settings{}, err
	}
	return f.Template.Settings()
}

// LibraryCounts are a library's job figures for its card.
type LibraryCounts struct {
	Working, Waiting, Failed int
	FreedBytes               int64
}

// Counts returns LibraryCounts by library id.
func (s *Store) Counts() (map[int64]LibraryCounts, error) {
	rows, err := s.db.Query(`SELECT library_id,
  SUM(status=?), SUM(status=?), SUM(status=?),
  COALESCE(SUM(CASE WHEN status=? AND size_after>0 AND output_path=path THEN size_before-size_after END),0)
FROM jobs WHERE library_id IS NOT NULL GROUP BY library_id`, Running, Queued, Failed, Done)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]LibraryCounts{}
	for rows.Next() {
		var id int64
		var c LibraryCounts
		if err := rows.Scan(&id, &c.Working, &c.Waiting, &c.Failed, &c.FreedBytes); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// Meta reads a value from app_meta ("" and false if it is not set).
func (s *Store) Meta(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM app_meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetMeta stores a value in app_meta.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
