package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/PeiceOfToast/shrink-your-shit"

// layers is the layer of each module package, by path relative to the module root.
var layers = map[string]int{
	"cmd/syp":             5,
	"internal/media":      0,
	"internal/media/fake": 0,
	"internal/events":     0,
	"internal/store":      0,
	"internal/settings":   0,
	"internal/fsx":        0,
	"internal/pathmap":    0,
	"internal/encode":     1,
	"internal/hw":         1,
	"internal/safety":     1,
	"internal/library":    2,
	"internal/jobs":       2,
	"internal/workers":    2,
	"internal/flow":       2,
	"internal/flow/steps": 2,
	"internal/worker":     3,
	"internal/remote":     3,
	"internal/notify":     3,
	"internal/system":     3,
	"internal/report":     3,
	"internal/api":        4,
	"internal/ui":         4,
}

// exceptions are the same-layer imports the plan allows, as "from -> to".
var exceptions = map[string]bool{
	"internal/events -> internal/store":     true,
	"internal/settings -> internal/store":   true,
	"internal/events -> internal/fsx":       true,
	"internal/store -> internal/fsx":        true,
	"internal/library -> internal/jobs":     true,
	"internal/library -> internal/flow":     true,
	"internal/flow/steps -> internal/flow":  true,
	"internal/flow -> internal/flow/steps":  true,
	"internal/media/fake -> internal/media": true,
	"internal/hw -> internal/encode":        true, // the S3-1 plan lists hw as a user of the encode plan
}

// TestPackageLayers reads the module's own source with go/parser rather than running `go list`. The test cache
// tracks the files a test process opens, so a changed import invalidates a cached pass. A subprocess would hide
// the change, and `go test` would report ok from cache after a bad import. Every non-test .go file counts,
// including ones behind other build tags, so the check is stricter than a single-platform build.
func TestPackageLayers(t *testing.T) {
	root := ".." // the module root
	imports := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			name := d.Name()
			skip := strings.HasPrefix(name, ".") && rel != "." || name == "node_modules" || name == "testdata" ||
				name == "vendor" || rel == "ui"
			if skip {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(filepath.Dir(rel))
		for _, spec := range f.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			imports[pkg] = append(imports[pkg], p)
		}
		if _, ok := imports[pkg]; !ok {
			imports[pkg] = nil
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read module source: %v", err)
	}

	var problems []string
	for from, imps := range imports {
		fromLayer, known := layers[from]
		if !known {
			problems = append(problems, "package "+from+" is not in the layer table")
			continue
		}
		for _, imp := range imps {
			if imp != module && !strings.HasPrefix(imp, module+"/") {
				continue
			}
			to := strings.TrimPrefix(strings.TrimPrefix(imp, module), "/")
			toLayer, known := layers[to]
			switch {
			case !known:
				problems = append(problems, from+" imports "+to+", which is not in the layer table")
			case toLayer > fromLayer:
				problems = append(problems, from+" (layer "+strconv.Itoa(fromLayer)+") imports "+to+" (layer "+strconv.Itoa(toLayer)+"), a higher layer")
			case toLayer == fromLayer && !exceptions[from+" -> "+to]:
				problems = append(problems, from+" imports "+to+", the same layer, and it is not in the exceptions")
			}
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}
