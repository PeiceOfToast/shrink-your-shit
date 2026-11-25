// Settings > Notifications (SCREENS V9f, mockups/settings-notifications.html): a message when files finish or need you.
// Contract "## Notifications" (UI-API-CONTRACT-v2.md). Each place has its own events; one summary setting serves them all.
import { useState } from "react";
import { Icon } from "../Icon";
import { failed, type Answer } from "../api";
import { SettingRow, ValueChip } from "../SettingRow";
import { useData } from "../useData";
import { clock } from "../format";
import { addTarget, getNotifications, patchNotifications, patchTarget, removeTarget, testNotification } from "../notificationsApi";
import type { NotifyEvents, NotifyKind, NotifySummary, NotifySummaryMode, NotifyTarget } from "../types/notifications";

// The order is the form's order and the target line's order. `short` is how the line names the event.
const EVENTS: { key: keyof NotifyEvents; label: string; hint?: string; short: string }[] = [
  { key: "jobFailed", label: "A file failed", short: "failed" },
  { key: "needsYou", label: "A file needs you", hint: "Held, or waiting for you to OK replacing it.", short: "needs you" },
  { key: "workerOffline", label: "A worker went offline", short: "worker offline" },
  { key: "batchFinished", label: "The summary", hint: "When it goes out is set under Summary below.", short: "summary" },
  { key: "encoderFellBack", label: "The graphics encoder failed on a file", hint: "The processor converted it instead, more slowly.", short: "encoder failed" },
  { key: "fileFinished", label: "A file finished", hint: "Can be a lot of messages; the summary is usually better.", short: "every finished file" },
];
const DEFAULT_EVENTS: NotifyEvents = { jobFailed: true, needsYou: true, fileFinished: false, batchFinished: true, workerOffline: true, encoderFellBack: false };
const MODES: { v: NotifySummaryMode; label: string }[] = [
  { v: "off", label: "Off" },
  { v: "drained", label: "When the queue is empty" },
  { v: "daily", label: "Every day" },
  { v: "weekly", label: "Every Monday" },
];
const dot = (s: string) => s.replace(/[.\s]+$/, "");
const sends = (e: NotifyEvents) => {
  const on = EVENTS.filter((x) => e[x.key]).map((x) => x.short);
  return on.length ? "Sends: " + on.join(", ") : "Sends: nothing. Turn an event on under Edit.";
};
const kindName = (k: NotifyKind) => (k === "discord" ? "Discord" : "Webhook");

type Tone = "ok" | "bad" | "warn";
function NoteLine({ tone, text }: { tone: Tone; text: string }) {
  return <div className={"note " + tone} role="status"><Icon id={tone === "ok" ? "i-check-c" : "i-x-c"} /><span>{text}</span></div>;
}

function Status({ t }: { t: NotifyTarget }) {
  if (!t.enabled) return <span className="st"><Icon id="i-pause" />Turned off</span>;
  const r = t.lastResult;
  if (!r) return <span className="st"><Icon id="i-clock" />Not sent yet</span>;
  if (r.ok) return <span className="st ok"><Icon id="i-check" />Last sent {clock(r.at)}</span>;
  return <span className="st warn"><Icon id="i-alert" /><span className="fail">Last send failed {clock(r.at)}: <span className="why">{dot(r.message)}.</span></span></span>;
}

function EventToggles({ value, onChange }: { value: NotifyEvents; onChange: (e: NotifyEvents) => void }) {
  return (
    <fieldset className="stack" style={{ border: 0, padding: 0, margin: 0 }}>
      <legend><strong>What to send here</strong></legend>
      {EVENTS.map((x) => (
        <label key={x.key} className="row nw" style={{ gap: 10, alignItems: "flex-start" }}>
          <input type="checkbox" role="switch" className="switch" checked={value[x.key]} onChange={(e) => onChange({ ...value, [x.key]: e.target.checked })} />
          <span><strong>{x.label}</strong>{x.hint && <><br /><span className="hint">{x.hint}</span></>}</span>
        </label>))}
      <p className="hint" style={{ margin: 0 }}>Messages less than a minute apart are grouped into one.</p>
    </fieldset>);
}

// Add a place (t undefined) or edit one. The kind can't change after saving; the address is write-only, so it shows
// as urlShown until "Change".
function TargetForm({ t, reload, onDone, onCancel }: { t?: NotifyTarget; reload: () => void; onDone: (text: string) => void; onCancel: () => void }) {
  const [kind, setKind] = useState<NotifyKind>("discord");
  const k = t?.kind ?? kind, id = "n-" + (t?.id ?? "new");
  const [name, setName] = useState(t?.name ?? "");
  const [url, setUrl] = useState("");
  const [changeUrl, setChangeUrl] = useState(!t);
  const [events, setEvents] = useState<NotifyEvents>(t?.events ?? DEFAULT_EVENTS);
  const [busy, setBusy] = useState(false), [result, setResult] = useState<{ tone: Tone; text: string } | null>(null), [error, setError] = useState("");
  const run = async <T,>(p: Promise<Answer<T>>): Promise<T | null> => {
    try { const r = await p; if (failed(r)) { setError(r.error.message); return null; } return r; } catch (_) { setError("Couldn't reach Shrink Your Shit. Try again."); return null; }
  };
  const test = async () => {
    setBusy(true); setError(""); setResult(null);
    // a plain Test of a saved place also sets its lastResult on the server, so the list is reloaded after it
    const r = await run(!t ? testNotification({ kind, url: url.trim() })
      : testNotification(changeUrl && url.trim() ? { id: t.id, url: url.trim() } : { id: t.id }));
    if (r) { setResult({ tone: r.ok ? "ok" : "bad", text: r.message }); if (t) reload(); }
    setBusy(false);
  };
  const save = async () => {
    setBusy(true); setError(""); setResult(null);
    const saved = await run(t
      ? patchTarget(t.id, { name: name.trim(), events, ...(changeUrl && url.trim() && { url: url.trim() }) })
      : addTarget({ name: name.trim(), kind, url: url.trim(), events }));
    setBusy(false);
    if (saved) onDone(`Saved ${saved.name}.`);
  };
  return (
    <form className="stack cform" onSubmit={(e) => { e.preventDefault(); save(); }} onInput={() => setResult(null)}>
      {!t && (
        <div className="seg" role="group" aria-label="Kind">
          <button type="button" aria-pressed={kind === "discord"} onClick={() => setKind("discord")}>Discord</button>
          <button type="button" aria-pressed={kind === "webhook"} onClick={() => setKind("webhook")}>Webhook (any app)</button>
        </div>)}
      <div className="field">
        <label htmlFor={id + "-name"}>Name</label>
        <input className="input" style={{ maxWidth: 320 }} id={id + "-name"} required value={name} onChange={(e) => setName(e.target.value)} placeholder={k === "discord" ? "#media" : "Home Assistant"} />
      </div>
      <div className="field">
        {changeUrl ? <label htmlFor={id + "-url"}>{k === "discord" ? "Webhook address" : "Address"}</label> : <span className="lab">{k === "discord" ? "Webhook address" : "Address"}</span>}
        {changeUrl
          ? <input className="input mono" style={{ maxWidth: 320 }} id={id + "-url"} type="password" autoComplete="off" spellCheck={false} required={!t} value={url}
            onChange={(e) => setUrl(e.target.value)} placeholder={t ? "Paste the new address" : ""} />
          : <p className="row" style={{ margin: 0, gap: 8 }} id={id + "-url"}><span className="mono">{t!.urlShown}</span>
            <button className="btn sm ghost" type="button" onClick={() => setChangeUrl(true)} aria-label="Change the address">Change</button></p>}
        <span className="hint">{k === "discord"
          ? "In Discord: Server settings › Integrations › Webhooks › New Webhook › Copy Webhook URL."
          : "We POST JSON. The fields are listed in the README."}</span>
      </div>
      <EventToggles value={events} onChange={setEvents} />
      <div aria-live="polite">{result && <NoteLine tone={result.tone} text={result.text} />}{error && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{error}</span></p>}</div>
      <div className="row" style={{ gap: 8 }}>
        <button className="btn" type="button" disabled={busy || ((!t || changeUrl) && !url.trim())} onClick={test}>Send a test</button>
        <button className="btn primary" type="submit" aria-label="Save destination" disabled={busy || !name.trim() || (!t && !url.trim())}>{busy ? "Working…" : "Save"}</button>
        <button className="btn ghost" type="button" onClick={onCancel} disabled={busy}>Cancel</button>
      </div>
    </form>);
}

// One place as a row of the Where to send card; the card body has no padding of its own, so the row's padding is the only
// indent (it used to add a second 16px inside the card, so the row sat off the card's other content).
function TargetItem({ t, first, reload, said }: { t: NotifyTarget; first: boolean; reload: () => void; said: (s: string) => void }) {
  const [editing, setEditing] = useState(false), [confirm, setConfirm] = useState(false), [testing, setTesting] = useState(false);
  const [note, setNote] = useState<{ tone: Tone; text: string } | null>(null), [msg, setMsg] = useState("");
  const test = async () => {
    setTesting(true); setNote(null);
    const r = await testNotification({ id: t.id });
    setTesting(false);
    if (failed(r)) { setNote({ tone: "bad", text: r.error.message }); return; }
    setNote({ tone: r.ok ? "ok" : "bad", text: r.message });
    reload(); // a plain Test sets the line's last result
  };
  const remove = async () => {
    const r = await removeTarget(t.id);
    if (failed(r)) { setMsg(r.error.message); return; }
    setConfirm(false); said(t.name + " removed. Nothing is sent there any more."); reload();
  };
  return (
    <div style={first ? undefined : { borderTop: "1px solid var(--border)" }}>
      <SettingRow title={<>{t.name} <ValueChip>{kindName(t.kind)}</ValueChip></>} desc={<><Status t={t} /><span className="hint" style={{ display: "block" }}>{sends(t.events)}</span></>}>
        {!editing && !confirm && (
          <>
            <button className="btn sm" type="button" onClick={test} disabled={testing}>{testing ? "Sending…" : "Send a test"}</button>
            <button className="btn sm" type="button" onClick={() => { setEditing(true); setNote(null); }} aria-label={"Edit " + t.name}>Edit</button>
            <button className="btn sm ghost" type="button" onClick={() => { setConfirm(true); setNote(null); }} aria-label={"Remove " + t.name}>Remove</button>
          </>)}
      </SettingRow>
      {(note || confirm || editing) && <div className="stack" style={{ padding: "0 var(--pad) 12px" }}>
        {note && <NoteLine tone={note.tone} text={note.text} />}
        {confirm && (
          <div className="row" role="group" aria-label={"Remove " + t.name} style={{ gap: 8 }}>
            <span>Remove {t.name}? Nothing is sent there any more.</span>
            <button className="btn sm danger" type="button" onClick={remove}>Remove</button>
            <button className="btn sm ghost" type="button" onClick={() => setConfirm(false)}>Cancel</button>
            {msg && <span className="st bad"><Icon id="i-alert" />{msg}</span>}
          </div>)}
        {editing && <TargetForm t={t} reload={reload} onCancel={() => setEditing(false)} onDone={(text) => { setEditing(false); said(text); reload(); }} />}
      </div>}
    </div>);
}

function AddPlace({ reload, said }: { reload: () => void; said: (s: string) => void }) {
  const [open, setOpen] = useState(false);
  return (
    <div style={{ padding: "12px var(--pad)" }}>
      {open
        ? <TargetForm reload={reload} onCancel={() => setOpen(false)} onDone={(text) => { setOpen(false); said(text); reload(); }} />
        : <button className="btn" type="button" onClick={() => setOpen(true)}><Icon id="i-plus" />Add a place to send</button>}
    </div>);
}

function SummaryCard({ s, reload, said }: { s: NotifySummary; reload: () => void; said: (m: string) => void }) {
  const [mode, setMode] = useState<NotifySummaryMode>(s.mode), [at, setAt] = useState(s.at);
  const [busy, setBusy] = useState(false), [error, setError] = useState("");
  const timed = mode === "daily" || mode === "weekly";
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setError("");
    const r = await patchNotifications({ summary: { mode, at } });
    setBusy(false);
    if (failed(r)) { setError(r.error.message); return; }
    said("Summary saved."); reload();
  };
  // its own Save: the summary goes through its own settings call, apart from the links
  return (
    <form className="bd" style={{ padding: 0 }} onSubmit={save}>
      <SettingRow title={<label htmlFor="n-sum">Send the summary</label>}
        desc={<><span style={{ display: "block" }}>It goes to the places with "The summary" on{mode === "drained" ? ", at most once an hour" : ""}.</span><span style={{ display: "block" }}>Example: "Shrink Your Shit: 42 files done, 3.1 GB freed, 2 failed, 1 needs you."</span></>}>
        <select id="n-sum" className="input sumsel" style={{ maxWidth: 280 }} value={mode} onChange={(e) => setMode(e.target.value as NotifySummaryMode)}>
          {MODES.map((m) => <option key={m.v} value={m.v}>{m.label}</option>)}
        </select>
        {timed && <div className="row" style={{ gap: 8 }}>
          <label htmlFor="n-at">At</label>
          <input id="n-at" className="input mono" type="time" value={at} onChange={(e) => setAt(e.target.value)} style={{ maxWidth: 120 }} />
        </div>}
      </SettingRow>
      <div style={{ padding: "0 var(--pad) 12px", display: "flex", flexDirection: "column", gap: 8, alignItems: "flex-end" }}>
        {error && <p className="st bad" role="alert" style={{ margin: 0, alignSelf: "stretch" }}><Icon id="i-alert" /><span>{error}</span></p>}
        <button aria-label="Save summary" className="btn" type="submit" disabled={busy}>{busy ? "Saving…" : "Save"}</button>
      </div>
    </form>);
}

function LinksCard({ linkBase, reload, said }: { linkBase: string | null; reload: () => void; said: (m: string) => void }) {
  // no address saved yet: the field starts with this browser's address (contract: the UI fills it from the browser)
  const [v, setV] = useState(linkBase ?? location.origin);
  const [busy, setBusy] = useState(false), [error, setError] = useState("");
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setError("");
    const r = await patchNotifications({ linkBase: v.trim() || null });
    setBusy(false);
    if (failed(r)) { setError(r.error.message); return; }
    said("Link saved."); reload();
  };
  return (
    <form className="bd" style={{ padding: 0 }} onSubmit={save}>
      <SettingRow title={<label htmlFor="n-base">Links in messages open</label>}
        desc="Filled from this browser's address. Leave it empty and messages have no links.">
        <input id="n-base" className="input mono" style={{ maxWidth: 320 }} type="url" inputMode="url" autoComplete="off" spellCheck={false} value={v} onChange={(e) => setV(e.target.value)} />
      </SettingRow>
      <div style={{ padding: "0 var(--pad) 12px", display: "flex", flexDirection: "column", gap: 8, alignItems: "flex-end" }}>
        {error && <p className="st bad" role="alert" style={{ margin: 0, alignSelf: "stretch" }}><Icon id="i-alert" /><span>{error}</span></p>}
        <button aria-label="Save links" className="btn" type="submit" disabled={busy}>{busy ? "Saving…" : "Save"}</button>
      </div>
    </form>);
}

export function NotificationsSettings() {
  const d = useData(getNotifications, []), [said, setSaid] = useState("");
  const notHere = d.error && (d.error.code === "http_404" || d.error.code === "not_found");
  return (
    <section id="notifications" aria-labelledby="h-where" className="stack settings-sec">
      <p className="muted" style={{ margin: 0 }}>Get a message when files finish or something needs you. Nothing is sent until you add a place to send it.</p>
      <p className="muted" role="status" style={{ margin: 0 }}>{said}</p>
      {notHere ? <p className="muted">This server can't send notifications yet.</p>
        : d.error && !d.data ? <p className="st bad" role="alert"><Icon id="i-alert" /><span>{d.error.message}</span></p>
          : !d.data ? <div className="empty">Loading…</div>
            : (
              <>
                <section className="card" aria-labelledby="h-where">
                  <div className="hd"><h2 id="h-where">Where to send</h2></div>
                  <div className="bd" style={{ padding: 0 }}>
                    {!d.data.targets.length && <p className="muted" style={{ margin: 0, padding: "12px var(--pad)" }}>No places yet.</p>}
                    {d.data.targets.map((t, i) => <TargetItem key={t.id} t={t} first={i === 0} reload={d.reload} said={setSaid} />)}
                    <AddPlace reload={d.reload} said={setSaid} />
                  </div>
                </section>
                <section className="card" aria-labelledby="h-sum">
                  <div className="hd"><h2 id="h-sum">Summary</h2></div>
                  <SummaryCard key={d.data.summary.mode + d.data.summary.at} s={d.data.summary} reload={d.reload} said={setSaid} />
                </section>
                <section className="card" aria-labelledby="h-links">
                  <div className="hd"><h2 id="h-links">Links in messages</h2></div>
                  <LinksCard key={d.data.linkBase ?? "none"} linkBase={d.data.linkBase} reload={d.reload} said={setSaid} />
                </section>
              </>)}
    </section>);
}
