# Blind comparison: design-claude vs Fable (app map + foundation review)

A separate reviewer judged the two versions without knowing who wrote which. It checked the claims against the code at dev 294e3d88.

## Verdict
design-claude's version is better overall, but Fable finds deeper root causes. The recommendation is to merge them, with design-claude's version as the base.

## Scorecard (1-5)

| | Fable | design-claude |
|---|---|---|
| Accuracy | 4 | 4 |
| Coverage | 4 | 4 |
| Root-cause insight | **5** | 4 |
| Plan quality | 3 | **5** |
| Readability (incl. diagrams) | 2 | **5** |

## Spot checks (12 citations each)

### Fable: 10 correct, 2 partly, 0 wrong
- The fallback ladder is cited at `failure_codes.go:33-54`. The real function is `jobs/encoderhealth.go:33`, which calls `classifyKeyLines`.
- "35 regexes" includes some that aren't ffmpeg-related (`backup.go`, `runlog_db.go`, `tempfile.go`).

### design-claude: 9 correct, 2 partly, 1 wrong
- It has the same `failure_codes.go` attribution slip as Fable.
- "The frame count is wrong for variable-frame-rate files" (`verify.go:724-729`) is plausible but not proven.
- **Wrong:** its job 48 example says the source was 640x360. The plan never scales up (`plan.go:848`); the real source was 1920x1080.

## Job 48: both find the real mechanism
- `ScaledWidth` rounds to the nearest even number (`rules/plan.go:1061-1066`).
- The QSV scale filter truncates (`plan.go:463`, from 0def124b on 11-20).
- So 1920x1080 scaled to 480 lines is predicted at 854 wide, and QSV makes 852.

What only Fable explains:
- `plan.Expect` is computed at `worker.go:1006`, BEFORE the encoder is chosen at `worker.go:1113-1145`.

What only design-claude points out:
- The refusal message (`expect.go:262`) labels the *predicted* 854x480 as "in the original", which points at the wrong cause.

## Diagrams (parsed with Mermaid 11.4.1)
- **Fable: all 8 fail to parse and would not render on GitHub.** The causes:
  - unquoted parentheses in node labels;
  - semicolons in sequence-diagram notes.

  Both are easy to fix.
- **design-claude: all 10 parse.** Every label is quoted.

## Fable: strengths
1. Two root causes design-claude lacks:
   - **One verdict covers three situations.** As a result, job 48 gets the wrong hint, "Pick a higher quality" (`failure_codes.go:133-135`).
   - **ffmpeg's end-of-run stats are already parsed** (`media/stats.go`), but only for the playback check (`verify.go:319`), because the encode runs at `-v error` (`plan.go:345`).
2. Tables: who calls whom, what differs per encoder path, and the source of truth for each fact.
3. A "what proves it" column for each migration slice, plus predictions of which test-matrix runs should fail.

## Fable: problems
1. Every diagram is broken on GitHub.
2. The job 48 fix only arrives after slice 1 (a medium-sized encoder-table refactor). There is no quick hotfix.
3. The prose is dense and full of jargon. There is no remote-worker flow diagram.

## design-claude: strengths
1. A plain-language explanation after every diagram, plus a table of contents. Easy for a non-expert.
2. A safe plan:
   - slice 1 is a small job 48 hotfix;
   - every slice has a size and its dependencies;
   - slice 9 changes behavior users would notice, so it is flagged as the user's decision.
3. Small real defects Fable missed, all verified:
   - **Picture-check score:** the stored SSIM score is the last sample's, not the lowest (`verify.go:491`).
   - **Totals:** `size_after` is written as 0 when the new file can't be read, which drops the job from the savings totals (`worker.go:481-484`).
   - **Wrong probe:** `LatestProbe` ignores size and time, so it can return the new file's probe instead of the original's (`jobs.go:504-508`).
   - **State machine:** `RequeueRunning` bypasses the job state machine (`jobs.go:431-440`).
   - **Workflow version:** `workflow_rev` is re-stamped on restart (`jobs.go:435`).

## design-claude: problems
1. The wrong 640x360 example for job 48.
2. The fix counts (~21/14/12/9/6) come from hand-sorting and can't be reproduced. Fable's figures (620 commits, 48 to `worker.go`) check out at 617 and 47.
3. It never says outright that the prediction is made before the encoder is chosen. Its fix is weaker on separating "different" from "broken".

## Merge recommendation
- **Base:** design-claude's structure, plain-language passages, diagrams, migration plan and "don't rewrite" section.
- **Add from Fable:**
  - the RC1 ordering argument, with the correct 1920x1080 example;
  - RC5 (one verdict for three situations);
  - RC4 (structured ffmpeg signals);
  - the per-encoder-path table and the source-of-truth table;
  - the "what proves it" column;
  - the test-matrix predictions.
- **Fix:** correct design-claude's 640x360 example to 1920x1080.
