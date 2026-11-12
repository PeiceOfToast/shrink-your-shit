// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNewerDatabase is returned by Open when the database was written by a newer version of the
// program than this one. Opening it anyway could corrupt it, so the program must be upgraded.
var ErrNewerDatabase = errors.New("this database is from a newer shrink-your-shit; upgrade the program")

// A migration is one numbered schema step. The schema version lives in SQLite's
// "PRAGMA user_version". To change the schema, ADD a step at the end with the next number;
// never edit or reorder a step that has been released. Steps must be deterministic.
type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{1, "baseline tables", `
CREATE TABLE IF NOT EXISTS files (
  path TEXT PRIMARY KEY, size INTEGER, mtime INTEGER, codec TEXT, decision TEXT);
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  library TEXT NOT NULL, path TEXT NOT NULL,
  status TEXT NOT NULL, progress REAL NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
  worker TEXT NOT NULL DEFAULT '', duration REAL NOT NULL DEFAULT 0,
  size_before INTEGER NOT NULL DEFAULT 0, size_after INTEGER NOT NULL DEFAULT 0,
  output_path TEXT NOT NULL DEFAULT '',
  created TEXT NOT NULL, updated TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status);`},
	// "IF NOT EXISTS" above is what lets a database made before migrations existed (version 0,
	// tables already there) be adopted as version 1 without touching its data.

	{2, "app_meta table", `
CREATE TABLE app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO app_meta(key, value) VALUES ('schema_created', datetime('now'));`},

	// Libraries and flows as records (slice 15). Libraries given as flags are added at startup
	// (EnsureLibraries), because their values come from the flags. A database that already holds
	// work is an upgrade ("install" = "upgrade", kept for good): its libraries keep converting as
	// before and the marker names their first flows "<folder> - Legacy". Otherwise it is a fresh
	// install: flag libraries wait for first-run (DESIGN/FIRST-RUN.md).
	{3, "libraries and flows", `
CREATE TABLE flows (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL, template TEXT NOT NULL, graph TEXT,
  created TEXT NOT NULL, changed TEXT NOT NULL);
CREATE UNIQUE INDEX flows_name ON flows(name);
CREATE TABLE libraries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL, path TEXT NOT NULL UNIQUE,
  flow_id INTEGER NOT NULL REFERENCES flows(id),
  started INTEGER NOT NULL DEFAULT 0, created TEXT NOT NULL);
ALTER TABLE jobs ADD COLUMN library_id INTEGER;
CREATE INDEX jobs_library ON jobs(library_id);
INSERT INTO app_meta(key, value) SELECT 'legacy_flows', '1'
  WHERE EXISTS (SELECT 1 FROM jobs) OR EXISTS (SELECT 1 FROM files);
INSERT INTO app_meta(key, value) SELECT 'install',
  CASE WHEN EXISTS (SELECT 1 FROM jobs) OR EXISTS (SELECT 1 FROM files) THEN 'upgrade' ELSE 'fresh' END;`},

	// Each job keeps the Plan it ran with (slice 16): what was intended, for the record and slice 17's
	// audit log; plan_fingerprint identifies its conversion settings.
	{4, "job plan snapshot", `
ALTER TABLE jobs ADD COLUMN plan TEXT;
ALTER TABLE jobs ADD COLUMN plan_fingerprint TEXT;`},

	// What ffprobe said about a file, while its size and modification time stay the same (slice 19):
	// the Preview reads a library without probing every file again, and the scanner fills it as it
	// goes. Separate from files, whose row means "the scanner has decided on this file".
	{5, "probe cache", `
CREATE TABLE probes (path TEXT PRIMARY KEY, size INTEGER NOT NULL, mtime INTEGER NOT NULL, info TEXT NOT NULL);`},

	// Originals kept in .syp-trash, and the journal of the swap that put them there (slice 17): a row is
	// committed before either rename, so a restart finishes or undoes any swap a crash interrupted.
	{6, "kept originals and swap journal", `
CREATE TABLE originals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id INTEGER NOT NULL, library_id INTEGER,
  path TEXT NOT NULL, trash TEXT NOT NULL, staged TEXT NOT NULL,
  original_bytes INTEGER NOT NULL, original_mtime INTEGER NOT NULL, original_hash TEXT NOT NULL,
  new_bytes INTEGER NOT NULL, new_mtime INTEGER NOT NULL DEFAULT 0, new_hash TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL, started TEXT NOT NULL, kept_at TEXT, delete_on TEXT);
CREATE INDEX originals_state ON originals(state);
CREATE INDEX originals_job ON originals(job_id);`},

	// Activity (slice 21b): when a job started (its time left is estimated from it), and a skipped
	// file's reason as a code (files.decision keeps the sentence).
	{7, "job start time and skip reason code", `
ALTER TABLE jobs ADD COLUMN started TEXT;
ALTER TABLE jobs ADD COLUMN output_mtime INTEGER NOT NULL DEFAULT 0;
ALTER TABLE files ADD COLUMN reason TEXT NOT NULL DEFAULT '';
CREATE INDEX files_reason ON files(reason);
CREATE INDEX jobs_output ON jobs(output_path, status);
UPDATE files SET reason = CASE
  WHEN decision = 'unreadable' THEN 'unreadable'
  WHEN decision LIKE 'skip: % is already an efficient format' THEN 'already_hevc'
  WHEN decision LIKE 'skip: codec is hevc,%' OR decision LIKE 'skip: codec is av1,%' OR decision LIKE 'skip: codec is vp9,%' THEN 'already_hevc'
  WHEN decision LIKE 'skip: codec is %' THEN 'format_not_converted'
  WHEN decision LIKE 'skip: % is not a format the slider converts' THEN 'format_not_converted'
  WHEN decision LIKE 'skip: bitrate % is below %' THEN 'already_efficient'
  WHEN decision LIKE 'skip: already efficient:%' THEN 'already_efficient'
  WHEN decision = 'skip: it has no video' THEN 'no_video'
  WHEN decision LIKE 'skip: its bitrate, picture size or frame rate is unknown%' THEN 'unknown_bitrate'
  WHEN decision LIKE 'skip: its % subtitles cannot be kept in %' THEN 'cannot_keep_track'
  ELSE '' END
WHERE decision = 'unreadable' OR decision LIKE 'skip:%';
-- a skip whose text maps to no code is forgotten: the next scan looks at the file again and records it
DELETE FROM files WHERE decision LIKE 'skip:%' AND reason = '';
-- jobs done before this: their output's time, from the file record at that path when its size is the
-- output's; otherwise it stays 0 (unknown), and such a job hides nothing from the Skipped list
UPDATE jobs SET output_mtime = (SELECT f.mtime FROM files f WHERE f.path=jobs.output_path AND f.size=jobs.size_after)
WHERE status='done' AND EXISTS (SELECT 1 FROM files f WHERE f.path=jobs.output_path AND f.size=jobs.size_after);`},

	// Slice 18: where a restore puts the converted file while the original goes back, a free name
	// allocated per restore and recorded before anything moves (only that recorded file is ever removed,
	// and only after its identity is checked).
	{8, "restore aside path", `
ALTER TABLE originals ADD COLUMN aside TEXT NOT NULL DEFAULT '';`},

	// Slice 24: "Stop this file" on a running job is recorded on its row, so the worker sees it at its
	// last check before the swap even if the stop reached no running worker (claims skip such rows).
	{9, "stop requested", `
ALTER TABLE jobs ADD COLUMN stop_requested INTEGER NOT NULL DEFAULT 0;`},

	// Slice 25b: subtitles saved as .srt files beside a new file, recorded before they are put there
	// with what they are (size, time, sample hash) and what the new file is, so a crash between them and
	// the swap leaves nothing of ours behind (worker.RecoverSidecars).
	{10, "srt sidecars", `
CREATE TABLE sidecars (id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL, path TEXT NOT NULL,
  bytes INTEGER NOT NULL, mtime INTEGER NOT NULL, hash TEXT NOT NULL,
  video_path TEXT NOT NULL, video_bytes INTEGER NOT NULL, video_hash TEXT NOT NULL);`},
	// 25a-2: a conversion in replace mode that changes the file type puts the new file at another name
	// (movie.mp4 -> movie.mkv); new_path is that name, '' when it is the original's (every older row).
	{11, "originals new_path", `ALTER TABLE originals ADD COLUMN new_path TEXT NOT NULL DEFAULT '';`},
	// DD-2: a failure that may pass (a slow disk, a full one, a busy file) is tried again later.
	// attempts: tries finished; retry_at: not taken before then ('' = now). Kept across restarts.
	{12, "job retries", `
ALTER TABLE jobs ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN retry_at TEXT NOT NULL DEFAULT '';`},
	// Always standardize (USER-REVIEW 2026-11-07): no minimum saving and no "already small" skip any more.
	// Once (as a migration it never runs again): jobs a check kept only because the new file saved too
	// little are queued again with their tries reset (no reason code is stored on a kept job, so its two
	// exact sentences are matched); file decisions of the removed skips are forgotten, so the next scan
	// looks at those files again and queues them.
	{13, "always standardize", `
UPDATE jobs SET status='queued', progress=0, worker='', error='', attempts=0, retry_at=''
WHERE status='kept' AND (error LIKE 'the new file (%) saves less than the required %% of the original (%)'
  OR error LIKE 'the new file (%) is not smaller than the original (%)');
DELETE FROM files WHERE reason IN ('already_efficient', 'would_not_shrink', 'unknown_bitrate');`},
	// S2, no auto-start (USER-REVIEW 2026-11-07): every install starts with its worker stopped (the paused
	// flag, which Claim honours). An existing install (this database had a version from 1 to 13 before
	// this run: temp.migrate_from) whose worker was running gets a one-time notice (stopped_by_update); a
	// new one does not. Not from its rows: migration 13 may have just removed some (review e1cd926).
	// (The notice's Basic sentence is S4's: its migration sets basic_changed_by_update.)
	{14, "workers start stopped", `
INSERT INTO app_meta(key, value) SELECT 'stopped_by_update', '1'
WHERE COALESCE((SELECT value FROM app_meta WHERE key='paused'), '0') <> '1'
  AND (SELECT version FROM temp.migrate_from) BETWEEN 1 AND 13;
INSERT INTO app_meta(key, value) VALUES ('paused', '1') ON CONFLICT(key) DO UPDATE SET value='1';`},
	// Slice A: the library card says when the last scan finished and how many videos it found.
	{15, "library last scan", `
ALTER TABLE libraries ADD COLUMN last_scan TEXT NOT NULL DEFAULT '';
ALTER TABLE libraries ADD COLUMN last_scan_videos INTEGER NOT NULL DEFAULT 0;`},
	// S4, Basic defaults: what an existing Basic (slider) flow does after the update. Design's call,
	// which the manager may overrule, so it is all in this one step: such a flow keeps every audio track
	// as it is (audio keep_all; a new Basic flow gets stereo AAC in the person's language) and now saves
	// MKV like every Basic flow (.mp4 and .avi files get a new name), and an existing install with one
	// gets the second sentence of the update notice (basic_changed_by_update, basicFlowsChanged). HEVC
	// files skipped as already_hevc are forgotten, so the next scan decides them again: Basic now
	// repackages those not yet in MKV (a Standard flow skips them again, as before).
	{16, "existing basic flows", `
UPDATE flows SET template = json_set(template, '$.params.audio', 'keep_all')
WHERE json_extract(template, '$.kind') = 'slider';
DELETE FROM files WHERE reason = 'already_hevc';
INSERT INTO app_meta(key, value) SELECT 'basic_changed_by_update', '1'
WHERE (SELECT version FROM temp.migrate_from) BETWEEN 1 AND 15
  AND EXISTS (SELECT 1 FROM flows WHERE json_extract(template, '$.kind') = 'slider');`},
	// S3, HDR10 kept (the user's decision, overturning D3): a Standard flow's skipHdr becomes
	// includeDolbyVision. Skipping HDR (the default) means leaving Dolby Vision as it is; a flow that
	// converted HDR and Dolby Vision includes Dolby Vision (profile 5 is protected whatever it says). Files
	// skipped as hdr_dv are forgotten, so the next scan decides them again (HDR10 is now converted).
	{17, "hdr10 kept", `
UPDATE flows SET template = json_set(json_remove(template, '$.selectors.skipHdr'), '$.selectors.includeDolbyVision',
  json(CASE WHEN json_extract(template, '$.selectors.skipHdr') = 0 THEN 'true' ELSE 'false' END))
WHERE json_extract(template, '$.kind') = 'selectors' AND json_type(template, '$.selectors') = 'object';
DELETE FROM files WHERE reason = 'hdr_dv';`},
	// S5, the Files view: each job's step summary (found, checked, converted, verified, kept, replaced),
	// in plain words. Its full run log is a file (internal/runlog). Rows of a job that is gone are
	// removed by the retention sweep (ForgetGoneJobSteps).
	{18, "job steps", `
CREATE TABLE IF NOT EXISTS job_steps (job_id INTEGER NOT NULL, step TEXT NOT NULL, at TEXT NOT NULL, ok INTEGER NOT NULL, words TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS job_steps_job ON job_steps(job_id);`},
	// S6, Standard rework: a Standard flow's saved values in S6's terms (rules.Selectors.Upgraded reads
	// unmigrated ones the same way, so nothing depends on this having run): Mbps caps become levels;
	// 25b's audio choices become design E's, with "Only my languages"; a flow whose audio result changes
	// (aac, opus, eac3, lossless_only) gets the notes line once. HEVC files skipped as already_hevc are
	// forgotten: Standard decides them again (repackaged, or already_in_format) (lead, 23:32Z).
	{19, "standard rework", `
UPDATE flows SET template = json_set(template, '$.selectors.maxBitrate',
  CASE json_extract(template, '$.selectors.maxBitrate') WHEN '40' THEN 'high' WHEN '20' THEN 'high' WHEN '10' THEN 'high'
    WHEN '6' THEN 'medium' WHEN '4' THEN 'medium' ELSE json_extract(template, '$.selectors.maxBitrate') END)
WHERE json_extract(template, '$.kind') = 'selectors' AND json_type(template, '$.selectors') = 'object';
UPDATE flows SET template = json_set(template,
  '$.selectors.onlyMyLanguages', json(CASE json_extract(template, '$.selectors.audio') WHEN 'my_languages' THEN 'true' ELSE 'false' END),
  '$.selectors.notes', json(CASE json_extract(template, '$.selectors.audio')
    WHEN 'aac' THEN '["Audio choices changed in this update: your flow now uses Stereo AAC."]'
    WHEN 'opus' THEN '["Audio choices changed in this update: your flow now uses Stereo Opus."]'
    WHEN 'eac3' THEN '["Audio choices changed in this update: your flow now uses Surround E-AC-3."]'
    WHEN 'lossless_only' THEN '["Audio choices changed in this update: your flow now uses Surround E-AC-3."]'
    ELSE '[]' END),
  '$.selectors.audio', CASE json_extract(template, '$.selectors.audio') WHEN 'keep_all' THEN 'as_is' WHEN 'my_languages' THEN 'as_is'
    WHEN 'aac' THEN 'stereo_aac' WHEN 'opus' THEN 'stereo_opus' WHEN 'eac3' THEN 'eac3_surround' WHEN 'lossless_only' THEN 'eac3_surround'
    ELSE json_extract(template, '$.selectors.audio') END)
WHERE json_extract(template, '$.kind') = 'selectors' AND json_type(template, '$.selectors') = 'object'
  AND json_extract(template, '$.selectors.audio') IN ('keep_all', 'my_languages', 'aac', 'opus', 'eac3', 'lossless_only');
UPDATE flows SET template = json_insert(json_set(template, '$.selectors.fileType', 'mkv'), '$.selectors.notes[#]',
  'File type changed to MKV: MP4 files can''t hold Opus.')
WHERE json_extract(template, '$.kind') = 'selectors' AND json_extract(template, '$.selectors.audio') = 'stereo_opus'
  AND json_extract(template, '$.selectors.fileType') = 'mp4';
DELETE FROM files WHERE reason = 'already_hevc';`},
	// V1 (UI rebuild): a flow becomes a workflow with revisions (one draft, every published one kept).
	// Each flow's revision 1 is written from its template when the store opens (EnsureRevisions), since
	// the workflow is made in Go; a flow saved by the old pages gets a new revision the same way.
	{20, "workflow revisions", `
ALTER TABLE flows ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE flows ADD COLUMN status TEXT NOT NULL DEFAULT 'enabled';
ALTER TABLE flows ADD COLUMN favourite INTEGER NOT NULL DEFAULT 0;
ALTER TABLE flows ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
CREATE TABLE workflow_revisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workflow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
  number INTEGER, status TEXT NOT NULL, doc TEXT NOT NULL, layout TEXT NOT NULL DEFAULT '{}',
  logic_hash TEXT NOT NULL, template_hash TEXT NOT NULL DEFAULT '', etag TEXT NOT NULL,
  created TEXT NOT NULL, published TEXT);
CREATE UNIQUE INDEX workflow_revisions_number ON workflow_revisions(workflow_id, number) WHERE number IS NOT NULL;
CREATE UNIQUE INDEX workflow_revisions_draft ON workflow_revisions(workflow_id) WHERE status = 'draft';`},
	// V2 (UI rebuild): every job's history (spec 7), written by triggers so no path can skip it: its
	// creation, each change of status (with the worker and the error at that moment), and its removal
	// (cancelled, "removed", unless Skip or a stop already wrote why). priority: the bulk action's, with
	// Claim's index.
	{21, "job history", `
ALTER TABLE jobs ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
CREATE TABLE job_events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, job_id INTEGER NOT NULL, at TEXT NOT NULL,
  from_status TEXT, to_status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', worker TEXT NOT NULL DEFAULT '');
CREATE INDEX job_events_job ON job_events(job_id);
CREATE TRIGGER job_events_insert AFTER INSERT ON jobs BEGIN
  INSERT INTO job_events(job_id, at, from_status, to_status) VALUES (new.id, new.created, NULL, new.status);
END;
CREATE TRIGGER job_events_status AFTER UPDATE OF status ON jobs WHEN old.status <> new.status BEGIN
  INSERT INTO job_events(job_id, at, from_status, to_status, reason, worker) VALUES (new.id, new.updated, old.status, new.status, new.error, new.worker);
END;
CREATE TRIGGER job_events_removed AFTER DELETE ON jobs
WHEN NOT EXISTS (SELECT 1 FROM job_events WHERE job_id = old.id AND to_status IN ('skipped', 'cancelled')) BEGIN
  INSERT INTO job_events(job_id, at, from_status, to_status, reason, worker)
  VALUES (old.id, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), old.status, 'cancelled', 'removed', old.worker);
END;
CREATE INDEX jobs_claim ON jobs(status, priority DESC, id);`},
	// V3a (live updates): one sequence for everything the event stream sends. job_events becomes events,
	// with a kind ("job" now; workflow, pause and activity events next) and job_id only for job events;
	// rows keep their seq, so ids already handed out stay valid, and the triggers write here.
	{22, "events", `
CREATE TABLE events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL DEFAULT 'job', job_id INTEGER, at TEXT NOT NULL,
  from_status TEXT, to_status TEXT, reason TEXT NOT NULL DEFAULT '', worker TEXT NOT NULL DEFAULT '', data TEXT);
INSERT INTO events(seq, kind, job_id, at, from_status, to_status, reason, worker)
  SELECT seq, 'job', job_id, at, from_status, to_status, reason, worker FROM job_events;
DROP TRIGGER job_events_insert;
DROP TRIGGER job_events_status;
DROP TRIGGER job_events_removed;
DROP TABLE job_events;
CREATE INDEX events_job ON events(job_id);
CREATE TRIGGER events_job_insert AFTER INSERT ON jobs BEGIN
  INSERT INTO events(kind, job_id, at, from_status, to_status) VALUES ('job', new.id, new.created, NULL, new.status);
END;
CREATE TRIGGER events_job_status AFTER UPDATE OF status ON jobs WHEN old.status <> new.status BEGIN
  INSERT INTO events(kind, job_id, at, from_status, to_status, reason, worker) VALUES ('job', new.id, new.updated, old.status, new.status, new.error, new.worker);
END;
CREATE TRIGGER events_job_removed AFTER DELETE ON jobs
WHEN NOT EXISTS (SELECT 1 FROM events WHERE kind = 'job' AND job_id = old.id AND to_status IN ('skipped', 'cancelled')) BEGIN
  INSERT INTO events(kind, job_id, at, from_status, to_status, reason, worker)
  VALUES ('job', old.id, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), old.status, 'cancelled', 'removed', old.worker);
END;`},
	// V3 (ui-shop, cap-778a2f0): a job's reason is {code, message}. The code was the sentence itself for
	// failures, kept originals and retries (jobs.error); it now has its own column, and the history keeps
	// both: reason the code, data the sentence. Older history: the sentence moves to data, and the code
	// comes from where the job went (ReasonCodes lists them all).
	{23, "reason codes", `
ALTER TABLE jobs ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
UPDATE jobs SET error_code = ` + legacyReasonCode("error", "status") + `
  WHERE error <> '';
UPDATE events SET data = json_object('message', reason),
  reason = ` + legacyReasonCode("reason", "to_status") + `
  WHERE kind = 'job' AND reason <> '' AND (reason GLOB '*[^a-z0-9_]*');
DROP TRIGGER events_job_status;
CREATE TRIGGER events_job_status AFTER UPDATE OF status ON jobs WHEN old.status <> new.status BEGIN
  INSERT INTO events(kind, job_id, at, from_status, to_status, reason, worker, data) VALUES ('job', new.id, new.updated, old.status, new.status,
    new.error_code, new.worker, CASE WHEN new.error <> '' THEN json_object('message', new.error) END);
END;
DROP TRIGGER events_job_removed;
CREATE TRIGGER events_job_removed AFTER DELETE ON jobs
WHEN NOT EXISTS (SELECT 1 FROM events WHERE kind = 'job' AND job_id = old.id AND to_status IN ('skipped', 'cancelled')) BEGIN
  INSERT INTO events(kind, job_id, at, from_status, to_status, reason, worker, data)
  VALUES ('job', old.id, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), old.status, 'cancelled', 'removed', old.worker,
    json_object('message', 'This job was removed from the list.'));
END;
UPDATE events SET data = json_object('message', 'This job was removed from the list.') WHERE kind = 'job' AND reason = 'removed' AND data IS NULL;
UPDATE events SET data = json_object('message', 'You stopped this file. It is left as it is until you press Convert again.')
  WHERE kind = 'job' AND reason = 'user_stopped' AND data IS NULL;`},
	// V4a (workers): what the person sets on a worker. The local one is "local"; remote ones come in V7.
	// Its processor slots are Settings' parallelJobs; gpu_slots NULL: the default (1 with a working
	// hardware encoder, else 0).
	{24, "workers", `
CREATE TABLE workers (id TEXT PRIMARY KEY, name TEXT NOT NULL, tags TEXT NOT NULL DEFAULT '[]', pool TEXT, gpu_slots INTEGER);`},
	// V5a (output-first): a library's own folder for new files with "Next to each library" ('' = the
	// default beside it). 25, not 24: V4a's workers table is 24.
	{25, "library output folder", `
ALTER TABLE libraries ADD COLUMN override_folder TEXT NOT NULL DEFAULT '';`},
	// V5b: a library's priority, which its new and waiting jobs take.
	{26, "library priority", `
ALTER TABLE libraries ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;`},
	// V5c (output-first, design call 3): when a scan found that a converted file's original, left where
	// it was, is gone (its saving counts as freed from then), or that it changed (it counts nowhere).
	{27, "original gone", `
ALTER TABLE jobs ADD COLUMN original_gone_at TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN original_changed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN original_disk TEXT NOT NULL DEFAULT '';`},
	// V6a (branches): a job's path through its workflow (node ids, JSON), and on each step the node it
	// was and the port the file left by (a check's answer).
	{28, "job path", `
ALTER TABLE jobs ADD COLUMN route TEXT NOT NULL DEFAULT '';
ALTER TABLE job_steps ADD COLUMN node_id TEXT NOT NULL DEFAULT '';
ALTER TABLE job_steps ADD COLUMN port TEXT NOT NULL DEFAULT '';`},
}

// legacyReasonCode is the SQL for the code of a reason stored before migration 23 as its sentence alone
// (column text), the job having gone to status (column state): the worker's own sentences say which
// case it was; any other gets the code of where the job went (review abe2b0e).
func legacyReasonCode(text, state string) string {
	return `CASE
    WHEN ` + text + ` LIKE 'there is not enough free disk space next to this file%' THEN 'low_disk_space'
    WHEN ` + text + ` LIKE 'The disk is full%' THEN 'disk_full'
    WHEN ` + text + ` LIKE 'shrink-your-shit is not allowed to read this file%' THEN 'cannot_read'
    WHEN ` + text + ` LIKE 'shrink-your-shit is not allowed to write in this folder%' THEN 'cannot_write'
    WHEN ` + text + ` LIKE 'Stopped: no progress for %' THEN 'stalled'
    WHEN ` + state + ` = 'failed' THEN 'convert_failed' WHEN ` + state + ` = 'kept' THEN 'kept_original' ELSE 'retry_later' END`
}

// keepBackups is how many pre-upgrade backups (<db>.bak-v<old version>) are kept.
const keepBackups = 3

func latestVersion() int { return migrations[len(migrations)-1].version }

func (s *Store) userVersion() (int, error) {
	var v int
	err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

// checkNotNewer refuses a database from a newer program, before anything is written to it.
func (s *Store) checkNotNewer() error {
	v, err := s.userVersion()
	if err != nil {
		return fmt.Errorf("read database version: %w", err)
	}
	if v > latestVersion() {
		return fmt.Errorf("%w (database schema version %d, this program understands up to %d)", ErrNewerDatabase, v, latestVersion())
	}
	return nil
}

// hasTables reports whether the database already holds data tables (an existing install).
func (s *Store) hasTables() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'`).Scan(&n)
	return n > 0, err
}

// migrate applies every step newer than the database's version, all in one transaction: if any
// step fails nothing is changed and Open fails. Before upgrading an existing database it saves a
// copy next to it.
func (s *Store) migrate() error {
	cur, err := s.userVersion()
	if err != nil {
		return fmt.Errorf("read database version: %w", err)
	}
	var pending []migration
	for _, m := range migrations {
		if m.version > cur {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	existing, err := s.hasTables()
	if err != nil {
		return err
	}
	backup := ""
	if existing {
		if backup, err = s.backup(cur); err != nil {
			return fmt.Errorf("could not back up the database before upgrading it (%v); not upgrading", err)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after a successful Commit
	// The version this database had before this run, for steps that treat an existing install
	// differently from a new one (14): a temporary table of this transaction's connection, dropped
	// below. A version-0 database with tables (from before migrations) is an existing install too.
	from := cur
	if from == 0 && existing {
		from = 1
	}
	if _, err := tx.Exec(`CREATE TEMP TABLE IF NOT EXISTS migrate_from (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM temp.migrate_from`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO temp.migrate_from(version) VALUES (?)`, from); err != nil {
		return err
	}
	for _, m := range pending {
		if _, err := tx.Exec(m.sql); err != nil {
			return failedMigration(m, err, backup)
		}
	}
	if _, err := tx.Exec(`DROP TABLE temp.migrate_from`); err != nil {
		return err
	}
	// PRAGMA takes no bound parameters; the number is ours. It is part of the same transaction.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", latestVersion())); err != nil {
		return failedMigration(pending[len(pending)-1], err, backup)
	}
	if err := tx.Commit(); err != nil {
		return failedMigration(pending[len(pending)-1], err, backup)
	}
	return nil
}

func failedMigration(m migration, err error, backup string) error {
	msg := fmt.Sprintf("database upgrade step %d (%s) failed: %v. The database was left as it was", m.version, m.name, err)
	if backup != "" {
		msg += "; a copy from before the upgrade is at " + backup
	}
	return errors.New(msg)
}

// backup saves a consistent copy of the database as <db>.bak-v<version> (replacing one from a
// previous attempt) and deletes all but the newest keepBackups of them.
func (s *Store) backup(version int) (string, error) {
	dest := fmt.Sprintf("%s.bak-v%d", s.path, version)
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	// VACUUM INTO writes a complete, consistent copy, including anything still in the WAL file.
	if _, err := s.db.Exec(`VACUUM INTO ?`, dest); err != nil {
		os.Remove(dest) // a failed copy (disk full...) must not be left looking like a backup
		return "", err
	}
	s.pruneBackups()
	return dest, nil
}

func (s *Store) pruneBackups() {
	matches, _ := filepath.Glob(s.path + ".bak-v*")
	type file struct {
		path string
		mod  int64
	}
	var files []file
	for _, m := range matches {
		if strings.HasSuffix(m, "-wal") || strings.HasSuffix(m, "-shm") {
			continue
		}
		if st, err := os.Stat(m); err == nil {
			files = append(files, file{m, st.ModTime().UnixNano()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	for i := keepBackups; i < len(files); i++ {
		os.Remove(files[i].path)
	}
}
