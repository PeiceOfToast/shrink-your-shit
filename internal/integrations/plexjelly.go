// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package integrations

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// jellyPage is how many items one listing of a library asks for. Each listing carries only the id and path of each.
const jellyPage = 500

// jellyUpdate is one entry of Jellyfin's POST /Library/Media/Updated.
type jellyUpdate struct {
	Path       string `json:"Path"`
	UpdateType string `json:"UpdateType"`
}

// plexSections is the part of GET /library/sections (JSON) that Test and the refresh read.
type plexSections struct {
	MediaContainer struct {
		Directory []struct {
			Key      string `json:"key"`
			Location []struct {
				Path string `json:"path"`
			} `json:"Location"`
		} `json:"Directory"`
	} `json:"MediaContainer"`
}

// jellyFolder is one media folder of GET /Library/VirtualFolders.
type jellyFolder struct {
	Name      string   `json:"Name"`
	ItemID    string   `json:"ItemId"`
	Locations []string `json:"Locations"`
}

// getJSON asks the instance for p and decodes the answer into v. It returns "" when that worked, or the message to
// show (tail is added when the instance did not answer).
func (s *Service) getJSON(ki kindInfo, base, key, p, tail string, v any) string {
	st, body, err := s.do(ki, "GET", base, key, p, nil)
	if err != nil {
		return unreached(ki, base, err, tail)
	}
	if !ok2(st) {
		return answer(ki, st)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return ki.label + " sent something the app doesn't understand."
	}
	return ""
}

// connected is the message of a Test that worked.
func connected(ki kindInfo, version *string) string {
	if version != nil {
		return fmt.Sprintf("Connected to %s %s.", ki.label, *version)
	}
	return "Connected to " + ki.label + "."
}

// plexVersion is the server's version as people read it: "1.41.3.9314-a0bfb8370" is "1.41.3".
func plexVersion(v string) string {
	parts := strings.SplitN(v, ".", 4)
	if len(parts) < 3 {
		return v
	}
	return strings.Join(parts[:3], ".")
}

// probePlex asks Plex for its version and its library sections; their locations are the folders.
func (s *Service) probePlex(ki kindInfo, base, key string, maps []Mapping) (TestResult, bool) {
	out := TestResult{Folders: []Folder{}}
	var id struct {
		MediaContainer struct {
			Version string `json:"version"`
		} `json:"MediaContainer"`
	}
	if msg := s.getJSON(ki, base, key, "/identity", "", &id); msg != "" {
		out.Message = msg
		return out, false
	}
	if v := plexVersion(id.MediaContainer.Version); v != "" {
		out.Version = &v
	}
	var secs plexSections
	if msg := s.getJSON(ki, base, key, "/library/sections", "", &secs); msg != "" {
		out.Message = msg
		return out, false
	}
	var paths []string
	for _, d := range secs.MediaContainer.Directory {
		for _, l := range d.Location {
			paths = append(paths, l.Path)
		}
	}
	out.Folders = s.mapFolders(paths, maps)
	out.OK = true
	out.Message = connected(ki, out.Version)
	return out, true
}

// probeJellyfin asks Jellyfin for its version and its media folders.
func (s *Service) probeJellyfin(ki kindInfo, base, key string, maps []Mapping) (TestResult, bool) {
	out := TestResult{Folders: []Folder{}}
	var info struct {
		Version string `json:"Version"`
	}
	if msg := s.getJSON(ki, base, key, "/System/Info", "", &info); msg != "" {
		out.Message = msg
		return out, false
	}
	if info.Version != "" {
		v := info.Version
		out.Version = &v
	}
	var folders []jellyFolder
	if msg := s.getJSON(ki, base, key, "/Library/VirtualFolders", "", &folders); msg != "" {
		out.Message = msg
		return out, false
	}
	var paths []string
	for _, f := range folders {
		paths = append(paths, f.Locations...)
	}
	out.Folders = s.mapFolders(paths, maps)
	out.OK = true
	out.Message = connected(ki, out.Version)
	return out, true
}

// dirOf is the folder holding p, in p's own separators ("/" for a file at the top).
func dirOf(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	switch {
	case i < 0:
		return ""
	case i == 0:
		return p[:1]
	}
	return p[:i]
}

// extOf is the lower-cased extension of p's last name ("" when it has none).
func extOf(p string) string {
	name := p[strings.LastIndexAny(p, `/\`)+1:]
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return ""
	}
	return strings.ToLower(name[i:])
}

// tellPlex asks each Plex section that holds the file's folder to scan that one folder. Plex answers at once and
// scans in the background. A folder no section holds is a warning, not a failure.
func (s *Service) tellPlex(ki kindInfo, name, base, key, file string) (summary string, ok, answered bool) {
	var secs plexSections
	if msg := s.getJSON(ki, base, key, "/library/sections", noNextScan, &secs); msg != "" {
		return msg, false, false
	}
	folder := dirOf(file)
	var sections []string
	for _, d := range secs.MediaContainer.Directory {
		for _, l := range d.Location {
			if _, ok := under(folder, l.Path); ok {
				sections = append(sections, d.Key)
				break
			}
		}
	}
	if len(sections) == 0 {
		return ki.label + " has no library for this folder; nothing to scan.", false, true
	}
	q := url.Values{"path": {folder}}.Encode()
	for _, k := range sections {
		st, _, err := s.do(ki, "GET", base, key, "/library/sections/"+url.PathEscape(k)+"/refresh?"+q, nil)
		if err != nil {
			return unreached(ki, base, err, noNextScan), false, false
		}
		if !ok2(st) {
			return answer(ki, st), false, false
		}
	}
	return fmt.Sprintf("Told Plex (%s) to scan the folder.", name), true, true
}

// tellJellyfin tells Jellyfin about the file. A file made new (old is empty: the original was kept) is Created. A
// replaced file whose extension changed has its old path Deleted and the new one Created, as the contract says.
// Otherwise it is Modified. Then its item is refreshed, because a replaced file keeps its modified time and Jellyfin
// would not probe it again.
func (s *Service) tellJellyfin(ki kindInfo, name, base, key, old, file string) (summary string, ok, answered bool) {
	var folders []jellyFolder
	if msg := s.getJSON(ki, base, key, "/Library/VirtualFolders", noNextScan, &folders); msg != "" {
		return msg, false, false
	}
	inLibrary, library := false, ""
	for _, f := range folders {
		for _, l := range f.Locations {
			if _, ok := under(file, l); ok {
				inLibrary = true
				if library == "" {
					library = f.ItemID
				}
			}
		}
	}
	if !inLibrary {
		return ki.label + " has no library for this folder; nothing to scan.", false, true
	}
	updates := []jellyUpdate{{Path: file, UpdateType: "Modified"}}
	switch {
	case old == "":
		updates = []jellyUpdate{{Path: file, UpdateType: "Created"}}
	case extOf(old) != extOf(file):
		updates = []jellyUpdate{{Path: old, UpdateType: "Deleted"}, {Path: file, UpdateType: "Created"}}
	}
	st, _, err := s.do(ki, "POST", base, key, "/Library/Media/Updated", map[string]any{"Updates": updates})
	if err != nil {
		return unreached(ki, base, err, noNextScan), false, false
	}
	if !ok2(st) {
		return answer(ki, st), false, false
	}
	if msg, good, answered := s.refreshJellyItem(ki, base, key, library, file); !good {
		return msg, false, answered
	}
	return fmt.Sprintf("Told Jellyfin (%s) the file changed.", name), true, true
}

// refreshJellyItem refreshes the one item whose file is file, so Jellyfin probes it again and shows the new size. A
// file Jellyfin has not indexed yet has no item, and then nothing is refreshed. Jellyfin searches item titles, and a
// release-named file's title is not its name, so the library's items are listed with their paths, a page at a time,
// and matched on the path. The walk stops at the page that holds the file.
func (s *Service) refreshJellyItem(ki kindInfo, base, key, library, file string) (msg string, good, answered bool) {
	if library == "" {
		return "", true, true
	}
	for start := 0; ; start += jellyPage {
		q := url.Values{"ParentId": {library}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie,Episode,Video"},
			"Fields": {"Path"}, "EnableImages": {"false"}, "EnableUserData": {"false"},
			"StartIndex": {strconv.Itoa(start)}, "Limit": {strconv.Itoa(jellyPage)}}
		var page struct {
			Items []struct {
				ID   string `json:"Id"`
				Path string `json:"Path"`
			} `json:"Items"`
			Total int `json:"TotalRecordCount"`
		}
		if msg := s.getJSON(ki, base, key, "/Items?"+q.Encode(), noNextScan, &page); msg != "" {
			return msg, false, false
		}
		for _, it := range page.Items {
			if !samePath(it.Path, file) {
				continue
			}
			rq := url.Values{"metadataRefreshMode": {"FullRefresh"}, "imageRefreshMode": {"None"},
				"replaceAllMetadata": {"false"}, "replaceAllImages": {"false"}}
			st, _, err := s.do(ki, "POST", base, key, "/Items/"+url.PathEscape(it.ID)+"/Refresh?"+rq.Encode(), nil)
			if err != nil {
				return unreached(ki, base, err, noNextScan), false, false
			}
			if !ok2(st) {
				return answer(ki, st), false, false
			}
			return "", true, true
		}
		if len(page.Items) == 0 || start+len(page.Items) >= page.Total {
			return "", true, true
		}
	}
}

// samePath says whether an item's path is the file's. A Windows-style path (backslashes or a drive letter) is
// compared without case and with either separator; any other path must match exactly.
func samePath(itemPath, file string) bool {
	if !windowsStyle(itemPath) && !windowsStyle(file) {
		return itemPath == file
	}
	return strings.EqualFold(strings.ReplaceAll(itemPath, `\`, "/"), strings.ReplaceAll(file, `\`, "/"))
}

// windowsStyle says whether p is a Windows path: it has a backslash or starts with a drive letter.
func windowsStyle(p string) bool {
	return strings.Contains(p, `\`) || (len(p) > 1 && p[1] == ':')
}
