// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// assetTypes are the Content-Types /assets/ sends, the same on every OS. Go otherwise reads them from the
// system (on Windows, the registry), which can say application/javascript or even text/plain for .js.
var assetTypes = map[string]string{
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".json": "application/json",
}

// gzipAssets wraps a handler of embedded files under prefix ("/assets/", "/next/"): it sets the
// Content-Type, and a browser that accepts gzip gets each file compressed (once, on first use, then
// kept); anything else goes to next as before.
func gzipAssets(prefix string, files fs.FS, next http.HandlerFunc) http.HandlerFunc {
	var cache sync.Map // file name -> gzip bytes
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		name := strings.TrimPrefix(r.URL.Path, prefix)
		if t, ok := assetTypes[path.Ext(name)]; ok {
			w.Header().Set("Content-Type", t) // both the file server and ServeContent keep a type already set
		}
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) || name == "" || strings.HasSuffix(name, "/") {
			next(w, r)
			return
		}
		gz, ok := cache.Load(name)
		if !ok {
			raw, err := fs.ReadFile(files, name)
			if err != nil { // missing, or a folder: next answers as it always has
				next(w, r)
				return
			}
			var b bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
			zw.Write(raw)
			zw.Close()
			gz, _ = cache.LoadOrStore(name, b.Bytes())
		}
		w.Header().Set("Content-Encoding", "gzip")
		// ServeContent sets Content-Type from the name, as the file server does, and handles HEAD.
		// Embedded files have no modification time, so no Last-Modified, the same as before.
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(gz.([]byte)))
	}
}

// acceptsGzip reads an Accept-Encoding header: gzip (or *) listed, and not with q=0.
func acceptsGzip(h string) bool {
	for _, part := range strings.Split(h, ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if enc = strings.ToLower(strings.TrimSpace(enc)); enc != "gzip" && enc != "*" {
			continue
		}
		q := strings.ReplaceAll(strings.ToLower(params), " ", "")
		return !(q == "q=0" || strings.HasPrefix(q, "q=0.") && strings.Trim(q[4:], "0") == "")
	}
	return false
}
