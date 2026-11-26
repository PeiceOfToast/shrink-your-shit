import { useState } from "react";
import { api, type NotifyMode, type NotifyTarget, type Notifications as NotifyData } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { LoadState, Row, SaveBar, Seg, Switch, asProblem, errText, useDirty, useDraft, type FieldProblem } from "./parts.tsx";

const MODES: { value: NotifyMode; label: string }[] = [
  { value: "off", label: "Off" },
  { value: "drained", label: "When the queue empties" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
];

type Draft = NotifyData;

function TargetCard({
  t,
  events,
  onChange,
  onDeleted,
}: {
  t: NotifyTarget;
  events: { code: string; name: string }[];
  onChange: (next: NotifyTarget) => void;
  onDeleted: () => void;
}) {
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const runTest = () => {
    setBusy(true);
    setTest(null);
    api.testTarget({ id: t.id }).then(
      (r) => setTest({ ok: r.ok, text: r.message }),
      (e: unknown) => setTest({ ok: false, text: errText(e) }),
    ).finally(() => setBusy(false));
  };
  const remove = () => {
    setBusy(true);
    api.deleteTarget(t.id).then(onDeleted, (e: unknown) => setTest({ ok: false, text: errText(e) })).finally(() => setBusy(false));
  };
  const toggleEvent = (code: string) =>
    onChange({ ...t, events: t.events.includes(code) ? t.events.filter((x) => x !== code) : [...t.events, code] });
  return (
    <section className="set-group">
      <div className="set-grouphead">
        <b>{t.name}</b> <span className="muted sm">{t.kind === "discord" ? "Discord" : "Webhook"} · {t.urlShown}</span>
        {t.failingSince && <span className="bad sm"> · failing</span>}
        <span className="sp" />
        <Switch label={`On: ${t.name}`} on={t.enabled} onChange={(enabled) => onChange({ ...t, enabled })} />
      </div>
      <div className="set-row full">
        <div className="set-lab"><label>Send these</label><div className="hint">Which events go to this target.</div></div>
        <div className="ticks">
          {events.map((e) => (
            <label key={e.code} className="row">
              <input type="checkbox" checked={t.events.includes(e.code)} onChange={() => toggleEvent(e.code)} /> {e.name}
            </label>
          ))}
        </div>
      </div>
      <div className="row">
        <button type="button" className="btn sm" onClick={runTest} disabled={busy}>Send a test</button>
        {test && <span className={test.ok ? "muted sm" : "bad sm"} role="status">{test.text}</span>}
        <span className="sp" />
        <button type="button" className="btn ghost sm" onClick={remove} disabled={busy}>Delete</button>
      </div>
    </section>
  );
}

function AddTarget({ onAdded }: { onAdded: () => void }) {
  const [kind, setKind] = useState<"webhook" | "discord">("webhook");
  const [url, setUrl] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const add = () => {
    setErr(null);
    api.createTarget({ kind, url }).then(
      () => {
        setUrl("");
        onAdded();
      },
      (e: unknown) => setErr(errText(e)),
    );
  };
  return (
    <section className="set-group">
      <div className="set-grouphead"><b>Add a target</b></div>
      <div className="set-row">
        <div className="set-lab"><span>Kind</span></div>
        <div className="set-ctl"><Seg label="Target kind" value={kind} onChange={setKind} options={[{ value: "webhook", label: "Webhook" }, { value: "discord", label: "Discord" }]} /></div>
      </div>
      <div className="set-row">
        <div className="set-lab"><label htmlFor="n-new-url">Address</label></div>
        <div className="set-ctl"><input id="n-new-url" className="inp" value={url} onChange={(e) => setUrl(e.target.value)} /></div>
      </div>
      {err && <p className="bad" role="alert">{err}</p>}
      <div className="row"><button type="button" className="btn primary sm" onClick={add} disabled={url === ""}>Add target</button></div>
    </section>
  );
}

// One draft for the whole page: the summary, the link base and every target. Save sends only what changed.
export function Notifications() {
  const s = useApi(() => api.getNotifications(), []);
  const ev = useApi(() => api.getNotifyEvents(), []);
  const d = useDraft<Draft>(s.data);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [problem, setProblem] = useState<FieldProblem | null>(null);
  const [text, setText] = useState<string | null>(null);
  useDirty("notifications", d.dirty);

  const draft = d.draft;
  if (!draft || !ev.data) return <><h2 className="set-title">Notifications</h2><LoadState s={s} /></>;
  const edit = (next: Draft) => {
    setSaved(false);
    setProblem(null);
    setText(null);
    d.setDraft(next);
  };
  const serverTargets = new Map(s.data?.targets.map((t) => [t.id, t]));
  const save = () => {
    const server = s.data;
    if (!server) return;
    setBusy(true);
    setProblem(null);
    setText(null);
    const summaryChanged = JSON.stringify(draft.summary) !== JSON.stringify(server.summary);
    const linkChanged = draft.linkBase !== server.linkBase;
    const changedTargets = draft.targets.filter((t) => JSON.stringify(t) !== JSON.stringify(serverTargets.get(t.id)));
    const jobs: Promise<unknown>[] = [];
    if (summaryChanged || linkChanged) {
      jobs.push(api.patchNotifications({ ...(summaryChanged ? { summary: draft.summary } : {}), ...(linkChanged ? { linkBase: draft.linkBase } : {}) }));
    }
    for (const t of changedTargets) {
      jobs.push(api.patchTarget(t.id, { name: t.name, enabled: t.enabled, events: t.events }));
    }
    Promise.all(jobs).then(
      () => {
        setSaved(true);
        s.reload();
      },
      (e: unknown) => {
        const r = asProblem(e);
        setProblem(r.problem);
        setText(r.text ?? errText(e));
      },
    ).finally(() => setBusy(false));
  };

  return (
    <>
      <h2 className="set-title">Notifications</h2>
      <Row id="n-sum" label="Send a summary" hint="How often a summary of finished work is sent." problem={problem}>
        <select id="n-sum" className="sel" value={draft.summary.mode} onChange={(e) => edit({ ...draft, summary: { ...draft.summary, mode: e.target.value as NotifyMode } })}>
          {MODES.map((m) => <option key={m.value} value={m.value}>{m.label}</option>)}
        </select>
      </Row>
      {draft.summary.mode !== "off" && draft.summary.mode !== "drained" && (
        <Row id="n-at" label="Summary time" hint="Local time the summary is sent." problem={problem} field="summary.at">
          <input id="n-at" className="inp" placeholder="08:00" value={draft.summary.at} onChange={(e) => edit({ ...draft, summary: { ...draft.summary, at: e.target.value } })} />
        </Row>
      )}
      <Row id="n-link" label="Address of this app" hint="Used to make links in messages. Leave empty to send names only." problem={problem} field="linkBase">
        <input id="n-link" className="inp" placeholder="http://192.168.1.20:8484" value={draft.linkBase ?? ""} onChange={(e) => edit({ ...draft, linkBase: e.target.value || null })} />
      </Row>

      {draft.targets.map((t) => (
        <TargetCard
          key={t.id}
          t={t}
          events={ev.data ?? []}
          onChange={(next) => edit({ ...draft, targets: draft.targets.map((x) => (x.id === t.id ? next : x)) })}
          onDeleted={s.reload}
        />
      ))}
      <AddTarget onAdded={s.reload} />
      <SaveBar dirty={d.dirty} busy={busy} saved={saved} text={text} onSave={save} onUndo={() => d.undo()} />
    </>
  );
}
