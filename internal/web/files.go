// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// filesRoutes is S5's Files view (UI-API-CONTRACT.md, "Workshop addition 21:00Z"): one list of files by
// card, and each job's run log. The old queue/skipped/failed/done/kept routes stay for one release.
func filesRoutes(api *http.ServeMux, store *jobs.Store, eng Engine) {
	api.HandleFunc("GET /api/activity/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, offset := pageOf(r)
		f := jobs.FileFilter{Show: q.Get("show"), Q: q.Get("q"), Kept: q.Get("kept") == "1", Limit: limit, Offset: offset}
		for _, c := range strings.Split(q.Get("reason"), ",") { // skipped: reason=a,b (contract 22:30Z)
			if c = strings.TrimSpace(c); c != "" {
				f.Reasons = append(f.Reasons, c)
			}
		}
		if f.Show == "" {
			f.Show = jobs.CardUnprocessed
		}
		if lib := q.Get("library"); lib != "" {
			id, err := strconv.ParseInt(lib, 10, 64)
			if err != nil || id <= 0 {
				apiError(w, http.StatusBadRequest, "invalid", "library must be a library's id.")
				return
			}
			f.LibraryID = id
		}
		rows, total, counts, err := store.FileList(f)
		if err != nil {
			if strings.HasPrefix(err.Error(), "show must be") {
				apiError(w, http.StatusBadRequest, "invalid", "show must be "+strings.Join(jobs.Cards, ", ")+".")
				return
			}
			serverError(w, err)
			return
		}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		keepDays := 7 // kept originals' days left follow the setting now, as the purge does
		if s, err := LoadSettings(store); err == nil {
			keepDays = s.KeepOriginalsDays
		}
		items := []map[string]any{}
		for _, row := range rows {
			items = append(items, fileItem(store, libs, row, f.Show, keepDays))
		}
		writeJSON(w, map[string]any{"items": items, "total": total, "counts": counts})
	})

	api.HandleFunc("GET /api/activity/files/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such file.")
			return
		}
		j, err := store.Get(id)
		if err != nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such file.")
			return
		}
		from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		text, next, err := logText(store, id, from)
		if err != nil {
			serverError(w, err)
			return
		}
		steps, err := store.Steps(id)
		if err != nil {
			serverError(w, err)
			return
		}
		if len(steps) == 0 { // a job from before S5: what its record says
			steps = append(steps, jobs.Step{Step: "found", At: j.Created, OK: true, Words: "Found in " + j.Library + "."})
			switch j.Status {
			case jobs.Done:
				if store.KeptBeside(id) {
					steps = append(steps, jobs.Step{Step: "kept", At: j.Updated, OK: true, Words: "The new file was written next to the original, which stays as it is."})
				} else {
					steps = append(steps, jobs.Step{Step: "replaced", At: j.Updated, OK: true, Words: "Converted."})
				}
			case jobs.Failed, jobs.Kept:
				steps = append(steps, jobs.Step{Step: "converted", At: j.Updated, OK: false, Words: j.Error})
			}
		}
		writeJSON(w, map[string]any{"summary": steps, "text": text, "next": next, "live": j.Status == jobs.Running})
	})
}

// pageOf reads ?limit= (default 50, at most 500) and ?offset=.
func pageOf(r *http.Request) (limit, offset int) {
	limit, offset = 50, 0
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 500)
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
		offset = n
	}
	return
}

// fileItem is one row of the Files view: what every card has, plus the card's own fields.
func fileItem(store *jobs.Store, libs []jobs.Library, row jobs.FileRow, card string, keepDays int) map[string]any {
	it := map[string]any{"path": row.Path, "folder": filepath.Dir(row.Path), "name": filepath.Base(row.Path),
		"libraryId": nil, "library": row.Library, "codec": nil, "resolution": nil, "bytes": row.SizeBefore, "durationSec": nil}
	for _, l := range libs { // its library (a skipped file: the one whose folder holds it)
		rel, err := filepath.Rel(l.Path, row.Path)
		if row.LibraryID > 0 && l.ID != row.LibraryID || err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		it["folder"] = filepath.ToSlash(filepath.Dir(rel)) // slice A's words: inside the library, "." for the top
		if row.LibraryID == 0 {
			it["library"] = l.Name
		}
		it["libraryId"] = strconv.FormatInt(l.ID, 10)
	}
	if row.LibraryID > 0 {
		it["libraryId"] = strconv.FormatInt(row.LibraryID, 10)
	}
	probed := row.Path
	if row.NewPath != "" {
		it["newPath"] = row.NewPath
		probed = row.NewPath
	}
	if card == jobs.CardProcessed && row.OutputPath != "" { // the new file's codec and size (Beside: its own name)
		probed = row.OutputPath
	}
	if card == jobs.CardProcessed && row.SizeAfter > 0 {
		it["bytes"] = row.SizeAfter
	}
	if raw, ok := store.LatestProbe(probed); ok {
		var info media.Info
		if json.Unmarshal([]byte(raw), &info) == nil {
			if v := info.Video(); v != nil {
				it["codec"] = preview.CodecWord(v.Codec) // slice A's words: "H.264"
				if v.Width > 0 && v.Height > 0 {
					it["resolution"] = preview.ResolutionWord(v.Width, v.Height) // "720p"
				}
			}
			if info.DurationSec > 0 {
				it["durationSec"] = info.DurationSec
			}
		}
	}
	jobID := strconv.FormatInt(row.JobID, 10)
	switch card {
	case jobs.CardSkipped:
		it["id"] = "skip-" + strconv.FormatInt(row.SkipID, 10)
		it["skippedId"] = strconv.FormatInt(row.SkipID, 10)
		detail := skipDetail(row.Reason, row.Decision)
		it["reason"], it["detail"] = row.Reason, upperFirst(detail)+"."
		return it
	case jobs.CardUnprocessed:
		it["status"], it["retryAt"], it["attempts"], it["detail"] = "waiting", nil, row.Attempts, nil
		if row.RetryAt != "" {
			it["status"], it["retryAt"], it["detail"] = "retrying", row.RetryAt, row.Error
		}
	case jobs.CardProcessing:
		it["jobId"], it["percent"] = jobID, 0.0
		it["etaSeconds"], it["convertingTo"], it["encoder"], it["decoder"], it["bitrateKbps"], it["fps"], it["worker"] = nil, nil, nil, nil, nil, nil, nil
		if l, ok := worker.Live(row.JobID); ok {
			p := l.Progress
			it["percent"], it["convertingTo"], it["encoder"], it["decoder"], it["worker"] = p.Percent, l.ConvertingTo, l.Encoder, l.Decoder, l.Worker
			if p.ETASeconds >= 0 {
				it["etaSeconds"] = int(p.ETASeconds + 0.5)
			}
			if p.BitrateKbps > 0 {
				it["bitrateKbps"] = int(p.BitrateKbps + 0.5)
			}
			if p.FPS > 0 {
				it["fps"] = p.FPS
			}
		}
	case jobs.CardProcessed:
		it["jobId"], it["beforeBytes"], it["afterBytes"], it["finishedAt"] = jobID, row.SizeBefore, row.SizeAfter, row.Updated
		it["flow"] = nil
		if row.FlowID > 0 { // the library's flow now (a job does not record which flow made it)
			it["flow"] = map[string]any{"id": strconv.FormatInt(row.FlowID, 10), "name": row.FlowName}
		}
		it["keptId"], it["keptDaysLeft"] = nil, nil
		switch {
		case row.OriginalState == jobs.OriginalKept:
			it["originalState"], it["keptId"] = "kept", strconv.FormatInt(row.OriginalID, 10)
			if due, ok := jobs.KeptUntil(row.OriginalKeptAt, keepDays); ok {
				it["keptDaysLeft"] = max(0, int(time.Until(due).Hours()/24+0.999))
			}
		case row.OriginalState == jobs.OriginalRestored:
			it["originalState"] = "restored"
		case row.OriginalState == jobs.OriginalPurged:
			it["originalState"] = "deleted"
		case row.Beside && filepath.Dir(row.OutputPath) != filepath.Dir(row.Path): // S6: saved to another folder
			it["originalState"], it["outputDir"] = "left_in_place", filepath.Dir(row.OutputPath)
		case row.Beside:
			it["originalState"] = "kept_beside"
		default:
			it["originalState"] = "deleted" // replaced with nothing kept (0 keep days, or before slice 18)
		}
	case jobs.CardFailed:
		it["jobId"], it["detail"], it["attempts"] = jobID, row.Error, max(row.Attempts, 1)
		it["title"] = "Converting did not finish"
		if row.Status == jobs.Kept {
			it["title"] = "A check said no, so the original was kept"
		}
	}
	it["id"] = jobID
	return it
}

// outputBlockers says why dir can't be a flow's folder for new files (S6, "Save to another folder";
// contract 21:10Z item 5): not_writable, low_space (under 10 GB free), inside_library (inside any
// library, so new files would be converted again; or a library inside it; V5a: every library, not only
// the flow's). Library except (0: none) is left out: it is being moved. Empty: it can be used.
func outputBlockers(store *jobs.Store, except int64, dir string, moreLibraries ...string) []map[string]string {
	out := []map[string]string{}
	add := func(code, msg string) { out = append(out, map[string]string{"code": code, "message": msg}) }
	if !filepath.IsAbs(dir) {
		add("not_writable", "Enter the folder's full path.")
		return out
	}
	dir = filepath.Clean(dir)
	real := resolved(dir)                                  // links followed, so an alias can't hide a folder inside a library (review 6cafc6f)
	if jobs.SystemFolder(dir) || jobs.SystemFolder(real) { // whoever the server runs as (ui-shop QA: root could write /etc)
		add("system_folder", "This is a system folder. Choose a folder for your videos.")
		return out
	}
	for _, lp := range append(moreLibraries, otherLibraries(store, except)...) { // moreLibraries: one being added or moved
		if outputInside(dir, lp) {
			add("inside_library", "This folder is inside the library, so new files would be converted again. Pick a folder outside "+lp+".")
			break
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		add("not_writable", "This folder can't be written to. Pick another one, or let shrink-your-shit write there.")
		return out
	}
	if f, err := os.CreateTemp(dir, ".syp-check-*"); err != nil {
		add("not_writable", "This folder can't be written to. Pick another one, or let shrink-your-shit write there.")
		return out
	} else {
		f.Close()
		os.Remove(f.Name())
	}
	free, ferr := worker.DiskFree(dir)
	size, serr := worker.DiskSize(dir)
	if ferr == nil && (free < 10<<30 || serr == nil && size > 0 && free < size/20) { // under 10 GB or 5% free
		add("low_space", fmt.Sprintf("This folder's disk has only %s free. New files need room: pick a folder on a disk with more space.", rules.HumanBytes(int64(free))))
	}
	return out
}

// inside reports whether path is dir itself or inside it.
func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolved is path with its links followed, as far as it exists (a folder for new files may not exist
// yet: its existing parent is resolved and the rest added back).
func resolved(path string) string {
	rest := ""
	for p := path; ; p = filepath.Dir(p) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(p) == p {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
	}
}

// folderRefused answers 400 when library lib (being added: ID 0; or moved: its new Path) would use
// flow flowID whose new files go to a folder inside or around it, or to none (V5a: "Next to each
// library" with no usable folder), or when lib's folder would hold another library's new files:
// checked when a library joins a flow or moves, not only when the flow is saved (review 6cafc6f).
func folderRefused(w http.ResponseWriter, store *jobs.Store, flowID int64, lib jobs.Library) bool {
	refuse := func(code, msg string) bool { apiError(w, http.StatusBadRequest, code, msg); return true }
	if f, err := store.GetFlow(flowID); err == nil {
		for _, part := range f.Template.Parts() { // each path of a workflow with checks saves its own way (V6a)
			dir, problem := libraryFolder(part, lib)
			if problem != nil {
				writeErrorWith(w, http.StatusBadRequest, problem.Code, problem.Message+" Nothing was changed.", map[string]any{"folder": problem.Folder, "reason": problem.Reason})
				return true
			}
			if b := outputInsideBlocker(dir, lib.Path); b != nil {
				return refuse(b.Code, b.Message)
			}
			for _, lp := range otherLibraries(store, lib.ID) {
				if dir != "" && outputInside(dir, lp) {
					return refuse("inside_library", "This flow saves this library's new files to "+dir+", which is inside the library at "+lp+" (or holds it), so new files would be converted again. Choose another folder. Nothing was changed.")
				}
			}
		}
	}
	libs, _ := store.Libraries() // the other libraries' folders for new files: never inside this one
	for _, m := range libs {
		if m.ID == lib.ID {
			continue
		}
		f, err := store.GetFlow(m.FlowID)
		if err != nil {
			continue
		}
		for _, part := range f.Template.Parts() {
			if dir, _ := libraryFolder(part, m); dir != "" && outputInside(dir, lib.Path) {
				return refuse("inside_library", lib.Path+" holds "+dir+", where "+m.Name+"'s new files go, so they would be converted again. Choose another folder. Nothing was changed.")
			}
		}
	}
	return false
}

// libraryFolder is where library l's new files go with template t ("" when t keeps them beside the
// original or replaces it), or why there is no folder (V5a: "Next to each library").
func libraryFolder(t rules.Template, l jobs.Library) (string, *jobs.FolderProblem) {
	s := t.Selectors
	switch {
	case s == nil || s.AfterConverting != "folder":
		return "", nil
	case s.FolderMode != rules.NextToLibrary:
		return s.OutputDir, nil
	case l.OverrideFolder != "":
		return l.OverrideFolder, nil
	}
	return jobs.NextToLibrary(l.Name, l.Path)
}

// otherLibraries are the paths of every library but except (0: none).
func otherLibraries(store *jobs.Store, except int64) []string {
	var out []string
	libs, _ := store.Libraries()
	for _, l := range libs {
		if l.ID != except {
			out = append(out, l.Path)
		}
	}
	return out
}

// outputInside reports whether new files saved to dir would land inside library folder lib, or around
// it (links followed, so an alias can't hide it, review 6cafc6f): they would be converted again.
func outputInside(dir, lib string) bool {
	real, lr := resolved(filepath.Clean(dir)), resolved(filepath.Clean(lib))
	return inside(real, lr) || inside(lr, real)
}

// outputInsideBlocker is the refusal of a library at lib whose flow saves new files to dir inside or
// around it (nil if not): adding or moving the library answers it, and so does POST /api/libraries/check
// with that folder, so first run can say it before the workflow is published (ui-shop 23:37Z).
func outputInsideBlocker(dir, lib string) *doctor.Issue {
	if !filepath.IsAbs(dir) || !outputInside(dir, lib) {
		return nil
	}
	return &doctor.Issue{Code: "inside_library", Message: "This flow saves new files to " + dir + ", which is inside this library (or holds it), so new files would be converted again. Choose another flow, or change the flow's folder. Nothing was changed."}
}

// skipDetail is a skipped file's reason in words: the fixed words for its code, except already_in_format,
// whose plan says the format, file type and audio it is already in (row 06:28Z); the stored decision
// for a code with no fixed words.
func skipDetail(reason, decision string) string {
	own := strings.TrimPrefix(decision, "skip: ")
	if w := rules.SkipWords[reason]; w != "" && (reason != rules.SkipAlreadyInFormat || own == decision || own == "") {
		return w
	}
	return own
}
