# Shrink Your Shit: new frontend (ui/)

React + TypeScript + Vite. Every screen gets its data through one module, `src/api/`.

## Run

    cd ui
    npm ci
    npm run dev          # http://localhost:5173, with the dev state switcher (?states=1)
    npm run typecheck
    npm test
    npm run build        # output in ../internal/ui/dist (gitignored except .gitkeep)

The build writes into the Go package that embeds it (`internal/ui/dist`), so build before
`go build`: a binary built without it serves the "not built" page from `internal/ui/notbuilt/` instead.
Run `npm run build` before committing anything under `internal/ui/`, and commit named files, never
`git add -A`: the build output is not tracked.

To look at a built copy on the server:

    npm run preview -- --host 10.10.2.201 --port 8090

## Mock and real API

`src/api/index.ts` decides which API is used. Today it is always the mock (`src/api/mock.ts`, fixtures in
`src/api/fixtures/`). When the backend is ready, only that module changes; pages do not import fixtures or fetch
directly.

Route-named functions (`getOverview()`, `getJobs(params)`, `getJob(id)`, ...) follow each page's "Data and API"
section in the Plan folder.

## Dev state switcher

In `npm run dev`, and in a preview with `?states=1`, a small bar puts the current page into empty, loading, error or
running states by driving the mock API.

## Routes

See `src/router.tsx`. Routes not built yet render a placeholder.
