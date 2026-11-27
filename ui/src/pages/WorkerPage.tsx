import { useEffect, useRef, useState } from "react";
import { Link, navigate } from "../router.tsx";
import { clock } from "../format/time.ts";
import { api, onSummaryChanged, type Job, type WorkerDetail, type WorkerLibrary } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { JobRow } from "./Jobs.tsx";
import { PauseDialog, RemoveDialog, StatePill, errorText } from "./workerParts.tsx";

// One worker's page (pages/10-workers.md, the worker panel). Changed fields only go to the server on Save.

// encoder is "auto" for automatic (sent as null), else "cpu" or an encoder name. Hours are "HH:MM" unless anyTime.
type Settings = { slots: number; gpuSlots: number; anyTime: boolean; from: string; to: string; encoder: string };
type Lib = { ticked: boolean; workerPath: string; mode: WorkerLibrary["mode"] };

const TEST_TEXT: Record<WorkerLibrary["test"]["state"], string> = {
  untested: "Not tested",
  pending: "Testing…",
  ok: "Works",
  failed: "Failed",
};

const hoursOf = (s: Settings) => (s.anyTime ? null : { from: s.from, to: s.to });

const settingsOf = (w: WorkerDetail): Settings => ({
  slots: w.slots,
  gpuSlots: w.gpuSlots,
  anyTime: w.hours === null,
  from: w.hours?.from ?? "09:00",
  to: w.hours?.to ?? "17:00",
  encoder: w.primaryEncoder ?? "auto",
});

export function WorkerPage({ id }: { id: string }) {
  const worker = useApi(() => api.getWorker(id), [id]);
  const libs = useApi(() => api.getLibraries(), []);
  const jobs = useApi(() => api.getJobs({ worker: id }), [id]);
  // The all-workers switch changes pausedGlobally here, so the page reads the worker again when it changes.
  useEffect(() => onSummaryChanged(worker.reload), [worker.reload]);
  const [saved, setSaved] = useState<Settings | null>(null);
  const [form, setForm] = useState<Settings | null>(null);
  const [libForm, setLibForm] = useState<Record<string, Lib>>({});
  const [libSaved, setLibSaved] = useState<Record<string, Lib>>({});
  const [tests, setTests] = useState<Record<string, WorkerLibrary["test"]>>({});
  const [dialog, setDialog] = useState<"pause" | "remove" | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  // Which Save was just done, so "Saved." shows beside that Save button.
  const [savedWhere, setSavedWhere] = useState<"settings" | "libs" | null>(null);

  const w = worker.data;

  const settingsDirty = !!form && !!saved && JSON.stringify(form) !== JSON.stringify(saved);
  const libsDirty = JSON.stringify(libForm) !== JSON.stringify(libSaved);
  // A refresh reads the worker again (the all-workers switch, a save elsewhere). A form with edits in progress keeps
  // its values through that refresh; only the state fields and the tests update.
  const dirty = useRef({ settings: false, libs: false });
  dirty.current = { settings: settingsDirty, libs: libsDirty };

  useEffect(() => {
    if (!w) return;
    if (!dirty.current.settings) {
      const s = settingsOf(w);
      setSaved(s);
      setForm(s);
    }
    if (!dirty.current.libs) {
      const byLib: Record<string, Lib> = {};
      for (const l of libs.data ?? []) {
        const have = w.libraries.find((x) => x.libraryId === l.id);
        byLib[l.id] = { ticked: !!have, workerPath: have?.workerPath ?? "", mode: have?.mode ?? "read_write" };
      }
      setLibSaved(byLib);
      setLibForm(byLib);
    }
    setTests(Object.fromEntries(w.libraries.map((x) => [x.libraryId, x.test])));
  }, [w, libs.data]);

  const run = async (fn: () => Promise<unknown>, after?: () => void) => {
    setBusy(true);
    setErr(null);
    setMsg(null);
    setSavedWhere(null);
    try {
      await fn();
      after?.();
    } catch (e) {
      setErr(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const reloadAll = () => {
    worker.reload();
    jobs.reload();
  };

  const saveSettings = () => {
    if (!form || !saved) return;
    const sent = form;
    const patch: Parameters<typeof api.patchWorker>[1] = {};
    if (form.slots !== saved.slots) patch.slots = form.slots;
    if (form.gpuSlots !== saved.gpuSlots) patch.gpuSlots = form.gpuSlots;
    if (JSON.stringify(hoursOf(form)) !== JSON.stringify(hoursOf(saved))) patch.hours = hoursOf(form);
    if (form.encoder !== saved.encoder) patch.primaryEncoder = form.encoder === "auto" ? null : form.encoder;
    if (Object.keys(patch).length === 0) return;
    run(() => api.patchWorker(id, patch), () => {
      // The saved copy is what was sent, so the form stops reading as dirty.
      setSaved(sent);
      setSavedWhere("settings");
      worker.reload();
    });
  };

  const tickedList = () =>
    Object.entries(libForm)
      .filter(([, l]) => l.ticked)
      .map(([libraryId, l]) => ({ libraryId, workerPath: l.workerPath.trim(), mode: l.mode }));
  const saveLibs = () => {
    const sent = libForm;
    run(() => api.patchWorker(id, { libraries: tickedList() }), () => {
      setLibSaved(sent);
      setSavedWhere("libs");
      worker.reload();
    });
  };

  const testPath = (libraryId: string) => {
    setTests((t) => ({ ...t, [libraryId]: { state: "pending" } }));
    api
      .testWorkerPath(id, libraryId)
      .then(() => api.waitForPathTest(id, libraryId))
      .then((res) => setTests((t) => ({ ...t, [libraryId]: res })))
      .catch((e: unknown) => setTests((t) => ({ ...t, [libraryId]: { state: "failed", message: errorText(e) } })));
  };

  const testEncoders = () =>
    run(async () => {
      const r = await api.testEncoders(id);
      setMsg(r.line);
    });

  if (!w) {
    return (
      <>
        <p className="muted">
          <Link to="/workers">← Workers</Link>
        </p>
        {worker.error && !worker.loading ? (
          <p className="bad">
            Can't load this worker.{" "}
            <button type="button" className="btn ghost sm" onClick={worker.reload}>Try now</button>
          </p>
        ) : (
          <span className="skel" style={{ height: 120 }} />
        )}
      </>
    );
  }

  const offline = w.state === "offline";
  // The choices are the encoders whose test passed, and the processor, which always works.
  const encoderChoices = [
    { value: "auto", label: "Choose automatically" },
    ...w.encoders.filter((e) => e.testOk).map((e) => ({ value: e.name, label: e.label })),
    { value: "cpu", label: "Processor" },
  ];

  return (
    <>
      <p className="muted sm" style={{ margin: "0 0 8px" }}>
        <Link to="/workers">← Workers</Link>
      </p>
      <div className="pagehead">
        <div style={{ minWidth: 0 }}>
          <h1>{w.name}</h1>
          {!w.local && <div className="muted sm">{w.address}</div>}
        </div>
        <span className="sp"></span>
        <StatePill w={w} />
        {!offline && w.state !== "paused" && (
          <button
            type="button"
            className={`btn sm${w.pausedGlobally ? " dis" : ""}`}
            onClick={() => setDialog("pause")}
            disabled={busy || w.pausedGlobally}
            title={w.pausedGlobally ? "Paused for all workers" : undefined}
          >
            Pause
          </button>
        )}
        {w.state === "paused" && (
          <button
            type="button"
            className={`btn sm${w.pausedGlobally ? " dis" : ""}`}
            onClick={() => run(() => api.resumeWorker(id), reloadAll)}
            disabled={busy || w.pausedGlobally}
            title={w.pausedGlobally ? "Paused for all workers" : undefined}
          >
            Resume
          </button>
        )}
        {offline && (
          <button type="button" className="btn bad sm" onClick={() => setDialog("remove")} disabled={busy}>Remove</button>
        )}
      </div>

      {offline && (
        <div className="note">
          <span>
            {w.name} is offline{w.since ? ` since ${clock(w.since)}` : ""}. Its jobs moved back to the queue.
          </span>
        </div>
      )}
      {err && <p className="err" role="alert">{err}</p>}
      {msg && <p className="muted sm" role="status">{msg}</p>}

      {form && (
        <section className="card">
          <h2 className="h3">Settings</h2>
          <div className="field">
            <label className="lbl" htmlFor="w-slots">Files at once</label>
            <input
              id="w-slots"
              className="inp narrow"
              type="number"
              min={1}
              max={8}
              value={form.slots}
              onChange={(e) => setForm({ ...form, slots: Number(e.target.value) })}
            />
          </div>
          <div className="field">
            <label className="lbl" htmlFor="w-gpu">Graphics slots</label>
            <input
              id="w-gpu"
              className="inp narrow"
              type="number"
              min={0}
              max={8}
              value={form.gpuSlots}
              onChange={(e) => setForm({ ...form, gpuSlots: Number(e.target.value) })}
              disabled={!w.gpu}
            />
            {w.gpu ? (
              <div className="hint">
                <span className="mono">{w.gpu.name}</span>
              </div>
            ) : (
              <div className="hint">No graphics card found, so files convert on the processor.</div>
            )}
          </div>
          <div className="field">
            <label className="lbl" htmlFor="w-anytime">Hours it may work</label>
            <label className="row" style={{ gap: 8 }}>
              <input
                id="w-anytime"
                type="checkbox"
                checked={form.anyTime}
                onChange={(e) => setForm({ ...form, anyTime: e.target.checked })}
              />
              Any time
            </label>
            {!form.anyTime && (
              <>
                <div className="row" style={{ marginTop: 8 }}>
                  <input
                    aria-label="From"
                    className="inp"
                    type="time"
                    value={form.from}
                    onChange={(e) => setForm({ ...form, from: e.target.value })}
                  />
                  <span className="muted">to</span>
                  <input
                    aria-label="To"
                    className="inp"
                    type="time"
                    value={form.to}
                    onChange={(e) => setForm({ ...form, to: e.target.value })}
                  />
                </div>
                <div className="hint">From later than To runs overnight, such as 22:00 to 06:00.</div>
              </>
            )}
          </div>
          <div className="field">
            <label className="lbl" htmlFor="w-enc">Graphics card or processor</label>
            <select id="w-enc" className="sel" value={form.encoder} onChange={(e) => setForm({ ...form, encoder: e.target.value })}>
              {encoderChoices.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            <div className="row">
              <button type="button" className="btn sm" onClick={testEncoders} disabled={busy}>Test the encoders</button>
            </div>
          </div>
          <div className="row" style={{ justifyContent: "flex-end" }}>
            {savedWhere === "settings" && <span className="muted sm" role="status">Saved.</span>}
            <button type="button" className="btn primary sm" onClick={saveSettings} disabled={!settingsDirty || busy}>Save</button>
          </div>
        </section>
      )}

      {!w.local && (
        <section className="card">
          <h2 className="h3">Libraries</h2>
          {(libs.data ?? []).length === 0 && <p className="muted">No libraries yet. Add one on Libraries, then tick it here.</p>}
          {(libs.data ?? []).map((l) => {
            const row = libForm[l.id];
            if (!row) return null;
            const t = tests[l.id] ?? { state: "untested" as const };
            // Test checks the saved path (page 10), so it waits until the field matches what is saved.
            const savedPath = w.libraries.find((x) => x.libraryId === l.id)?.workerPath ?? "";
            const unsaved = row.workerPath.trim() !== savedPath.trim();
            const failed = row.ticked && t.state === "failed" && t.message;
            const waitingForSave = row.ticked && unsaved && !!row.workerPath.trim();
            return (
              <div className="librow" key={l.id}>
                <label className="librow-name">
                  <input
                    type="checkbox"
                    checked={row.ticked}
                    onChange={(e) => setLibForm({ ...libForm, [l.id]: { ...row, ticked: e.target.checked } })}
                  />{" "}
                  <b>{l.name}</b>
                  <span className="librow-path muted xs mono">{l.path}</span>
                </label>
                <div className="librow-field">
                  <input
                    aria-label={`Path on ${w.name} for ${l.name}`}
                    className="inp mono"
                    placeholder="Path on this worker"
                    value={row.workerPath}
                    disabled={!row.ticked}
                    onChange={(e) => setLibForm({ ...libForm, [l.id]: { ...row, workerPath: e.target.value } })}
                  />
                </div>
                <div className="librow-act">
                  <button
                    type="button"
                    className="btn sm"
                    onClick={() => testPath(l.id)}
                    disabled={!row.ticked || !row.workerPath.trim() || unsaved || t.state === "pending"}
                  >
                    Test
                  </button>
                  {row.ticked && (
                    <span className={`pill ${t.state === "ok" ? "ok" : t.state === "failed" ? "bad" : t.state === "pending" ? "run" : ""}`}>
                      {TEST_TEXT[t.state]}
                    </span>
                  )}
                </div>
                {(failed || waitingForSave) && (
                  <div className="librow-msg xs">
                    {failed && <span className="bad">{t.message}</span>}
                    {waitingForSave && <span className="muted">Save to test this path</span>}
                  </div>
                )}
              </div>
            );
          })}
          {w.newLibraries.length > 0 && (
            <p className="muted sm">New: {w.newLibraries.map((l) => l.name).join(", ")}, not taken yet.</p>
          )}
          <div className="row" style={{ justifyContent: "flex-end" }}>
            {savedWhere === "libs" && <span className="muted sm" role="status">Saved.</span>}
            <button type="button" className="btn primary sm" onClick={saveLibs} disabled={!libsDirty || busy}>Save</button>
          </div>
        </section>
      )}
      {w.local && <p className="muted">Takes every library, at the same paths.</p>}

      <section className="card" style={{ padding: 0 }}>
        <div className="row" style={{ padding: "12px 14px" }}>
          <h2 className="h3" style={{ margin: 0 }}>Recent jobs</h2>
          <span className="sp"></span>
          <Link to={`/settings/log?worker=${id}`}>System log</Link>
        </div>
        {jobs.error && !jobs.data && <p className="bad pad">Can't load this worker's jobs.</p>}
        {jobs.data && jobs.data.items.length === 0 && <p className="muted pad">No jobs yet.</p>}
        {jobs.data && jobs.data.items.length > 0 && (
          <table className="tbl wk-jobs">
            <thead>
              <tr>
                <th>Status</th>
                <th>File</th>
                <th>Step</th>
                <th className="num">Progress</th>
                <th className="num">When</th>
                <th className="num">Result</th>
              </tr>
            </thead>
            <tbody>
              {jobs.data.items.slice(0, 10).map((j: Job) => (
                <JobRow key={j.id} j={j} filter="" />
              ))}
            </tbody>
          </table>
        )}
      </section>

      {dialog === "pause" && (
        <PauseDialog w={w} onClose={() => setDialog(null)} onDone={() => { setDialog(null); reloadAll(); }} />
      )}
      {dialog === "remove" && (
        <RemoveDialog w={w} onClose={() => setDialog(null)} onDone={() => navigate("/workers")} />
      )}
    </>
  );
}
