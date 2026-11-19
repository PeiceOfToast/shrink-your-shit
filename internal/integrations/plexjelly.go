// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package integrations

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
