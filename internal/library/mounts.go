// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"bufio"
	"io"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// A network share mounted twice gets two device numbers, so the device identity misses it (S2-10a). For a
// network file system the mount table says where each folder really is: the share's source (server and
// export) and the path inside the share. Two folders with one source and one path are one folder, however
// many mounts they came through. Other OSes have no mount table to read, so only the identity check runs there.

// Mount is one line of the mount table: where a file system is mounted and what it is.
type Mount struct {
	Point  string // the mount point, unescaped
	Root   string // the path inside the file system that is mounted there
	FSType string // nfs, cifs, ext4 and so on
	Source string // the device or share: //server/share, server:/export, /dev/sda1
}

// networkFSTypes are the file systems whose source names a share. Local disks are not compared by source.
var networkFSTypes = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "fuse.sshfs": true,
}

// ParseMountInfo reads /proc/self/mountinfo as the kernel writes it. A line that does not parse is
// skipped rather than failing the whole table: one odd line must not hide the other mounts.
func ParseMountInfo(r io.Reader) ([]Mount, error) {
	var out []Mount
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// The optional fields end at a lone "-"; after it come the type, the source and the superblock options.
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if sep < 5 || len(fields) < sep+3 {
			continue
		}
		out = append(out, Mount{
			Root:   unescapeMountField(fields[3]),
			Point:  unescapeMountField(fields[4]),
			FSType: fields[sep+1],
			Source: unescapeMountField(fields[sep+2]),
		})
	}
	return out, sc.Err()
}

// unescapeMountField undoes the kernel's octal escapes: a space in a mount point is \040.
func unescapeMountField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Share is a folder by its place on a network share: the share's normalised source and the path inside it.
type Share struct {
	Source string
	Path   string
}

// same reports whether two folders are one folder on one share.
func (s Share) same(o Share) bool { return s.Source == o.Source && s.Path == o.Path }

// inside reports whether s is somewhere below o on the same share.
func (s Share) inside(o Share) bool {
	if s.Source != o.Source {
		return false
	}
	if o.Path == "/" {
		return s.Path != "/"
	}
	return strings.HasPrefix(s.Path, o.Path+"/")
}

// ShareOf places a real folder on its network share, using the longest mount point that holds it. It reports
// false for a folder on a local disk, or when no mount holds it: those keep the identity check alone.
//
// The share's path is the export the mount came from, then the path inside the mounted file system, then the
// folder's own path below the mount point. So a subfolder of an export mounted directly and the same folder
// through a mount of the subfolder's export are one share path.
func ShareOf(mounts []Mount, real string) (Share, bool) {
	best := -1
	for i, m := range mounts {
		if !networkFSTypes[m.FSType] || !holds(m.Point, real) {
			continue
		}
		if best < 0 || len(m.Point) > len(mounts[best].Point) {
			best = i
		}
	}
	if best < 0 {
		return Share{}, false
	}
	m := mounts[best]
	rel, err := filepath.Rel(m.Point, real)
	if err != nil {
		return Share{}, false
	}
	host, export := splitSource(m.FSType, m.Source)
	return Share{
		Source: host,
		Path:   path.Clean("/" + path.Join(export, m.Root, filepath.ToSlash(rel))),
	}, true
}

// holds reports whether the mount point is the folder or one of its ancestors.
func holds(point, real string) bool {
	if point == real {
		return true
	}
	if point == "/" {
		return strings.HasPrefix(real, "/")
	}
	return strings.HasPrefix(real, point+"/")
}

// splitSource divides a mount source into its host and its export path, lowering what is case-insensitive
// and leaving the rest exactly as written. Host names and SMB shares are case-insensitive, and trailing slashes
// are not part of a path. DNS is not resolved.
//   - cifs and smb3 write //server/share: the host is //server and the share is the export, both lowered.
//   - nfs, nfs4 and sshfs write host:/export: only the host is lowered, because NFS export paths are case-sensitive.
func splitSource(fstype, source string) (host, export string) {
	switch fstype {
	case "cifs", "smb3":
		rest := strings.TrimPrefix(source, "//")
		h, share, _ := strings.Cut(rest, "/")
		return "//" + strings.ToLower(h), "/" + strings.ToLower(share)
	default:
		h, exp, ok := strings.Cut(source, ":")
		if !ok {
			return source, "/"
		}
		return strings.ToLower(h), exp
	}
}

// readMounts is the mount table, or nil when it cannot be read: a missing table skips the share check and is not an
// error.
func readMounts() []Mount {
	r, err := mountInfoSource()
	if err != nil {
		return nil
	}
	defer r.Close()
	mounts, err := ParseMountInfo(r)
	if err != nil {
		return nil
	}
	return mounts
}
