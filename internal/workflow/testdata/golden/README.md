# Golden workflows

- `*.json` builder goldens: written from today's builders by `WORKFLOW_GOLDEN_WRITE=1 go test -run TestGolden`.
- `rev1-pre-r8-save.json`: raw bytes the app stored at a73fd894 (migration 20), a Save with no ifBigger, from before FillCommitDefaults.
- `rev1-legacy-template.json`: raw bytes the app stored at fcd01ab9^, a revision 1 of a legacy template with a video.legacy_settings node.
- `fingerprints.json`: the plan fingerprint each golden compiles to on a 1920x1080 sample.
