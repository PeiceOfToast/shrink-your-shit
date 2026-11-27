package internal_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
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
	"internal/events":     0,
	"internal/store":      0,
	"internal/fsx":        0,
	"internal/pathmap":    0,
	"internal/encode":     1,
	"internal/hw":         1,
	"internal/safety":     1,
	"internal/library":    2,
	"internal/jobs":       2,
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
	"internal/events -> internal/store":    true,
	"internal/events -> internal/fsx":      true,
	"internal/store -> internal/fsx":       true,
	"internal/library -> internal/jobs":    true,
	"internal/library -> internal/flow":    true,
	"internal/flow/steps -> internal/flow": true,
	"internal/flow -> internal/flow/steps": true,
}

type listedPackage struct {
	ImportPath string
	GoFiles    []string
	Imports    []string
}

func TestPackageLayers(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-json", "./...")
	cmd.Dir = ".." // the module root
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("go list: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("go list: %v", err)
	}

	var problems []string
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode go list: %v", err)
		}
		if p.ImportPath != module && !strings.HasPrefix(p.ImportPath, module+"/") {
			continue // standard library and dependencies
		}
		if len(p.GoFiles) == 0 {
			continue // a folder with only test files, such as this one
		}
		from := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, module), "/")
		fromLayer, known := layers[from]
		if !known {
			problems = append(problems, "package "+from+" is not in the layer table")
			continue
		}
		for _, imp := range p.Imports {
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
