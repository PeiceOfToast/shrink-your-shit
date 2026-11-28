// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Goldens for the job routes, from the live answers of the fixture. The UI's contract check compares each body
// against its exported type in types.ts. The neighbours answer has no exported type, so it has no golden yet.
// The fixture's clocks are fixed and its library folder is shown as /videos, so the goldens are the same each run.
func TestContractGoldensForJobs(t *testing.T) {
	f := newJobsFixture(t)
	scrub := func(rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		out.Code = rec.Code
		out.Body.WriteString(strings.ReplaceAll(rec.Body.String(), f.videos, "/videos"))
		return out
	}
	// The log commits on the sink's flush, so wait until the done job's lines are stored before writing its golden.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(f.get(fmt.Sprintf("/api/v2/jobs/%d/events", f.done)).Body.String(), `"kind"`) {
			break
		}
	}
	writeGolden(t, "jobs-list", "JobsPage", http.MethodGet, "/api/v2/jobs", scrub(f.get("/api/v2/jobs")))
	writeGolden(t, "job-get", "JobDetail", http.MethodGet, fmt.Sprintf("/api/v2/jobs/%d", f.done),
		scrub(f.get(fmt.Sprintf("/api/v2/jobs/%d", f.done))))
	writeGolden(t, "job-events", "LogEvent[]", http.MethodGet, fmt.Sprintf("/api/v2/jobs/%d/events", f.done),
		scrub(f.get(fmt.Sprintf("/api/v2/jobs/%d/events", f.done))))
	writeGolden(t, "job-history", "HistoryLine[]", http.MethodGet, fmt.Sprintf("/api/v2/jobs/%d/history", f.done),
		scrub(f.get(fmt.Sprintf("/api/v2/jobs/%d/history", f.done))))
	// The actions answer (S3-6a): one id done and one refused, from the live route. Move to the top changes
	// the waiting job's pick order, which no other golden shows.
	writeGolden(t, "jobs-actions", "JobsActionDone", http.MethodPost, "/api/v2/jobs/actions",
		f.e.do(http.MethodPost, "/api/v2/jobs/actions",
			fmt.Sprintf(`{"action":"top","ids":[%d,%d]}`, f.waiting, f.done),
			write(map[string]string{"Cookie": f.cookie.String()}), false))
}
