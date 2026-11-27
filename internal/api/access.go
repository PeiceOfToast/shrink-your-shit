// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// The access group: the first-run page's reads (languages, starters, the guide, the folder browser)
// and its folder check. They call the packages and never SQL.

// starterRow is page 05's Starter card.
type starterRow struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Line   string `json:"line"`
	Icon   string `json:"icon"`
	Guided bool   `json:"guided"`
}

// starterDetail is the starter and the answers the guided page starts from.
type starterDetail struct {
	starterRow
	Answers map[string]any `json:"answers"`
}

func starterRowOf(s flow.Starter) starterRow {
	return starterRow{ID: s.ID, Kind: s.Kind, Name: s.Name, Line: s.Line, Icon: s.Icon, Guided: s.Guided}
}

func (s *Server) getLanguages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, flow.Languages())
}

func (s *Server) getStarters(w http.ResponseWriter, r *http.Request) {
	out := []starterRow{}
	for _, st := range flow.Starters() {
		out = append(out, starterRowOf(st))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getStarter(w http.ResponseWriter, r *http.Request) {
	st, ok := flow.StarterByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That starting workflow doesn't exist.", "", 0)
		return
	}
	writeJSON(w, http.StatusOK, starterDetail{starterRow: starterRowOf(st), Answers: flow.DefaultAnswers()})
}

func (s *Server) getGuide(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, flow.Guide())
}

// folderListing is page 09's FolderListing: the folder's path, its parent (null at the root) and the
// folders it holds, by name. Files are never listed.
type folderListing struct {
	Path    string        `json:"path"`
	Parent  *string       `json:"parent"`
	Folders []folderEntry `json:"folders"`
}

type folderEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Server) getFolders(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			path = home
		} else {
			path = "/"
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		writeFolderProblem(w, err)
		return
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		writeFolderProblem(w, err)
		return
	}
	// Say what is wrong before listing: on Windows ReadDir on a file answers "path not found", which
	// would read as a missing folder instead of a file.
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "invalid_request", "That is not a folder. Choose a folder.", "path", 0)
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeFolderProblem(w, err)
		return
	}
	listing := folderListing{Path: abs, Folders: []folderEntry{}}
	if parent := filepath.Dir(abs); parent != abs {
		listing.Parent = &parent
	}
	for _, e := range entries {
		child := filepath.Join(abs, e.Name())
		if !e.IsDir() {
			// A link to a folder is still a folder to the person browsing. A link to anything else,
			// or a broken link, is not.
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			if info, err := os.Stat(child); err != nil || !info.IsDir() {
				continue
			}
		}
		listing.Folders = append(listing.Folders, folderEntry{Name: e.Name(), Path: child})
	}
	sort.Slice(listing.Folders, func(i, j int) bool {
		return strings.ToLower(listing.Folders[i].Name) < strings.ToLower(listing.Folders[j].Name)
	})
	writeJSON(w, http.StatusOK, listing)
}

// writeFolderProblem answers a folder the person cannot use: missing is a 404; a file, an unreadable
// folder or another bad path is a 400 with the sentence for the field.
func writeFolderProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "not_found", "That folder isn't there.", "path", 0)
	case errors.Is(err, os.ErrPermission):
		writeError(w, http.StatusBadRequest, "invalid_request", "That folder can't be read. Choose another one.", "path", 0)
	default:
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			writeError(w, http.StatusBadRequest, "invalid_request", "That is not a folder. Choose a folder.", "path", 0)
			return
		}
		internalError(w)
	}
}

// checkBody is "Check this folder": a folder on this computer, or a share (not in stage 1).
type checkBody struct {
	Path    string `json:"path"`
	ShareID string `json:"shareId"`
}

// folderCheck is page 09's FolderCheck: the videos a folder holds and what Add would refuse.
type folderCheck struct {
	Videos    int             `json:"videos"`
	SizeBytes int64           `json:"sizeBytes"`
	Problems  []folderProblem `json:"problems"`
}

type folderProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) checkLibraryFolder(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	var body checkBody
	if !decode(w, r, &body) {
		return
	}
	if body.ShareID != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Shares aren't available yet. Choose a folder on this computer.", "shareId", 0)
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Pick the folder with the videos.", "path", 0)
		return
	}
	check, err := rs.Catalog.CheckFolder(r.Context(), body.Path)
	if err != nil {
		var pathErr *library.PathError
		switch {
		case errors.Is(err, library.ErrMissing):
			writeError(w, http.StatusNotFound, "not_found", "That folder isn't there.", "path", 0)
		case errors.As(err, &pathErr):
			writeError(w, http.StatusBadRequest, "invalid_request", pathErr.Error(), "path", 0)
		case errors.Is(err, library.ErrPathAlreadyWatched):
			writeError(w, http.StatusBadRequest, "invalid_request", "Another library already watches this folder. Choose a different folder.", "path", 0)
		default:
			internalError(w)
		}
		return
	}
	out := folderCheck{Videos: check.Videos, SizeBytes: check.SizeBytes, Problems: []folderProblem{}}
	for _, p := range check.Problems {
		out.Problems = append(out.Problems, folderProblem{Code: p.Code, Message: p.Message})
	}
	writeJSON(w, http.StatusOK, out)
}
