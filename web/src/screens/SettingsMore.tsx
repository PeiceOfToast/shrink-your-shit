// Settings > General and Safety (SCREENS V9) on the real server's settings (v1's, at /api/v2/settings): the old pages'
// settings and words (settings.json), the new interface's look. General: Your languages, the access token (shown only
// when asked for, never kept by the page; a new one; Sign out, only when the server needs a token). Safety: how long
// replaced originals are kept, where, and what the trash holds now (its whole size lives here, not beside Space saved:
// design 05:34Z). Not here: log retention (no setting on the server).
import { useEffect, useId, useRef, useState, type Dispatch, type MouseEvent, type ReactNode, type SetStateAction } from "react";
import { Icon } from "../Icon";
import { SettingRow, ValueChip } from "../SettingRow";
import { SignedOut, failed, type Answer } from "../api";
import { canForget, fileName, nextForgetId, reasonLine, unreachableLine } from "../unreachable";
import { bytes, plural } from "../format";
import { cancelUpdate, checkUpdate, getConverting, getHealth, getUpdate, installUpdate, forgetOriginal, purgeTrash, rollbackUpdate, type UpdateNotice, getSettings, getToken, getTrash, patchSettings, rotateToken, signOut } from "../libraryApi";
import type { PurgeResult, Unreachable } from "../types/v2";
import type { FirstRunSettings } from "../types/v1";
import { Link, navigate } from "../router";
import { setRole } from "../setupApi";
import { useSetup } from "./Enroll";
import { useData } from "../useData";
import { holdLost } from "../live";
import { ModeChoice, PasswordFields } from "../SignInChoice";
import { getAuth, onAuthChanged, setAuthMode } from "../signinApi";
import { accessTokenWords, authWord, canSave, passwordProblem, type AuthMode, type AuthState } from "../signinRules";

async function act<T>(p: Promise<Answer<T>>, say: (m: string) => void): Promise<T | null> {
  try { const r = await p; if (failed(r)) return (say(r.error.message), null); return r; } catch (e) { if (e instanceof SignedOut) return null; throw e; }
}
const day = (t: string) => new Date(t).toLocaleDateString([], { day: "numeric", month: "short" });

export function GeneralSettings() {
  const st = useData(getSettings, []);
  return (
    <section id="general" aria-labelledby="pc-h" className="stack settings-sec">
      {!st.data ? <div className="empty">{st.error?.message ?? "Loading…"}</div> : <General start={st.data.languages ?? []} auth={!!st.data.authRequired} saved={st.reload} />}
    </section>);
}

// The page's one Save (the design): it saves the languages; the rows above only change what is on the page until then.
function General({ start, auth, saved }: { start: string[]; auth: boolean; saved: () => void }) {
  const [codes, setCodes] = useState(start), [said, setSaid] = useState(""), [bad, setBad] = useState(false);
  const save = async () => { setBad(false); if (await act(patchSettings({ languages: codes }), (m) => (setBad(true), setSaid(m)))) (setSaid("Saved."), saved()); };
  const dirty = JSON.stringify(codes) !== JSON.stringify(start);
  return (<>
    <section className="card" aria-labelledby="pc-h"><div className="hd"><h2 id="pc-h">This computer</h2></div>
      <div className="bd stack" style={{ padding: 0, gap: 0 }}>
        <ThisComputer />
        <Languages codes={codes} setCodes={setCodes} say={(m, isBad) => (setSaid(m), setBad(!!isBad))} />
      </div></section>
    <section className="card" aria-labelledby="acc-h"><div className="hd"><h2 id="acc-h">Access</h2></div>
      <div className="bd stack" style={{ padding: 0, gap: 0 }}><Access auth={auth} /></div></section>
    <div className="stack" style={{ gap: 8 }}>
      <p className={bad ? "st bad" : "muted"} role="status" style={{ margin: 0 }}>{said}</p>
      <div className="row" style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 12 }}>
        <button className="btn primary" type="button" aria-label="Save general settings" disabled={!dirty} onClick={save}>Save</button>
        <span className="muted">Languages are saved when you press Save.</span>
      </div>
    </div>
  </>);
}

// This computer (ONE-COMMAND-SETUP.md section 11): main server or a worker, and Change... with a confirm. Server to
// worker clears the role and opens the choice page without Main server; worker to server starts setup. While jobs
// convert the server refuses (409 role_change_busy). Hidden on an older server without /api/v2/setup.
function ThisComputer() {
  const su = useSetup(), s = su.data;
  const [ask, setAsk] = useState(false), [busy, setBusy] = useState(false), [err, setErr] = useState<ReactNode>(null);
  const btn = useRef<HTMLButtonElement>(null);
  if (!s) return null;
  const worker = s.role === "worker", name = s.join?.serverName ?? "the other server";
  const go = async () => {
    setBusy(true); setErr(null);
    let r; try { r = await setRole(worker ? { role: "server" } : { role: "unset" }); } catch (e) { setBusy(false); if (e instanceof SignedOut) return; throw e; }
    setBusy(false); setAsk(false);
    if (!failed(r)) return navigate(worker ? "/start" : "/start?worker=1");
    const n = (r.error as { runningJobs?: number }).runningJobs;
    setErr(r.error.code !== "role_change_busy" ? r.error.message
      : <>{n ? plural(n, "job is", "jobs are") : "Jobs are"} converting now. Stop {n === 1 ? "it" : "them"} on <Link to="/jobs">Jobs</Link> first, or wait.</>);
    btn.current?.focus();
  };
  return (<>
    <SettingRow title="Role" desc={s.role === "server" ? "Keeps your libraries and this web page." : undefined}>
      <ValueChip>{s.role === "server" ? "Main server" : worker ? "Worker for " + name : "Not chosen yet"}</ValueChip>
      <button ref={btn} className="btn" type="button" onClick={() => (s.role === "unset" ? navigate("/start") : (setErr(null), setAsk(true)))}>Change…</button>
    </SettingRow>
    {err && <p className="st bad" role="alert" style={{ margin: "0 16px 12px" }}><Icon id="i-alert" /><span>{err}</span></p>}
    {ask && (worker
      ? <Confirm title={"Stop being a worker for " + name + "?"} body={name + " stops sending jobs here. This computer becomes its own server and starts setup."}
          yes="Make it a server" no="Keep as worker" busy={busy} onYes={go} onNo={() => (setAsk(false), btn.current?.focus())} />
      : <Confirm title="Make this computer a worker?" body="Its libraries and workflows stop running here. Nothing is deleted. Switch back in Settings to use them again."
          yes="Make it a worker" no="Keep as server" busy={busy} onYes={go} onNo={() => (setAsk(false), btn.current?.focus())} />)}
  </>);
}

// A small modal confirm (the folder picker's dialog look): focus starts on the title, Tab stays inside, Escape keeps.
function Confirm({ title, body, yes, no, busy, danger, onYes, onNo }: { title: string; body: string; yes: string; no: string; busy: boolean; danger?: boolean; onYes: () => void; onNo: () => void }) {
  const id = useId(), box = useRef<HTMLDivElement>(null), head = useRef<HTMLHeadingElement>(null);
  useEffect(() => { head.current?.focus(); }, []);
  return (
    <div className="fpick small" onMouseDown={(e) => e.target === e.currentTarget && !busy && onNo()}>
      <div ref={box} className="dlg" role="dialog" aria-modal="true" aria-labelledby={id + "-t"} aria-describedby={id + "-b"} onKeyDown={(e) => {
        if (e.key === "Escape") return (e.stopPropagation(), !busy && onNo());
        if (e.key !== "Tab") return;
        const f = [...box.current!.querySelectorAll<HTMLElement>("button:not(:disabled)")], i = f.indexOf(document.activeElement as HTMLElement), last = f.length - 1;
        if (e.shiftKey ? i <= 0 : i === -1 || i === last) { e.preventDefault(); (e.shiftKey ? f[last] : f[0])?.focus(); }
      }}>
        <h2 id={id + "-t"} ref={head} tabIndex={-1}>{title}</h2>
        <p id={id + "-b"}>{body}</p>
        <div className="row enroll-btns"><button className={danger ? "btn danger" : "btn primary"} type="button" disabled={busy} onClick={onYes}>{yes}</button>
          <button className="btn" type="button" disabled={busy} onClick={onNo}>{no}</button></div>
      </div>
    </div>);
}

// "Your languages": one list for the whole install, shown by name, saved as ISO 639-2 codes (/languages.json:
// [code, name, name in the language]); added by typing a name and picking it.
// The codes and the one Save live in General; this row reports what it did through say().
function Languages({ codes, setCodes, say }: { codes: string[]; setCodes: Dispatch<SetStateAction<string[]>>; say: (m: string, bad?: boolean) => void }) {
  const [list, setList] = useState<[string, string, string?][]>([]), [typed, setTyped] = useState(""), [open, setOpen] = useState(false), [at, setAt] = useState(-1), [adding, setAdding] = useState(false);
  const addBtn = useRef<HTMLButtonElement>(null), box = useRef<HTMLInputElement>(null), backToAdd = useRef(false);
  useEffect(() => { fetch("/languages.json").then((r) => r.json()).then(setList, () => {}); }, []);
  // the box takes focus when it opens; closing it (Enter, a pick, Add, Escape) puts focus back on "+ Add"
  useEffect(() => {
    if (adding) return box.current?.focus();
    if (backToAdd.current) { backToAdd.current = false; addBtn.current?.focus(); }
  }, [adding]);
  const name = (c: string) => list.find((l) => l[0] === c)?.[1] ?? c;
  const q = typed.trim().toLowerCase();
  const hits = q ? list.filter((l) => [l[1], l[2] ?? "", l[0]].some((s) => s.toLowerCase().includes(q))) : list;
  const close = () => (setAdding(false), setOpen(false), setAt(-1), setTyped(""), (backToAdd.current = true));
  const add = (raw = typed) => {
    const t = raw.trim().toLowerCase(); if (!t) return;
    const hit = list.find((l) => l[1].toLowerCase() === t || (l[2] ?? "").toLowerCase() === t || l[0] === t);
    if (!hit) return say("No language called " + raw.trim() + ". Pick one from the list.", true);
    setCodes((cs) => (cs.includes(hit[0]) ? cs : [...cs, hit[0]]));
    close(); say(hit[1] + " added. Press Save to keep it.");
  };
  const pick = (l: [string, string, string?]) => add(l[1]);
  return (
    <SettingRow title="Your languages" descId="langs-h" desc={<>Audio and subtitles in these languages are kept when a workflow says "Keep only my languages". Tracks with no language set are always kept. "Save as separate .srt files" saves subtitles in these languages as files.</>}>
      <div className="langs">
        {codes.length ? <ul className="chips" aria-label="Your languages" style={{ listStyle: "none", padding: 0, margin: 0, flexWrap: "wrap" }}>{codes.map((c) => (
          <li key={c} className="mchip removable">{name(c)}<button className="btn sm icon ghost" type="button" aria-label={"Remove " + name(c)} onClick={() => (setCodes(codes.filter((x) => x !== c)), say(name(c) + " removed. Press Save to keep it."))}><Icon id="i-x" /></button></li>))}</ul>
          : <p className="muted" style={{ margin: 0 }}>None yet.</p>}
        {adding ? (
          // Escape closes the box from anywhere in it (the field, a list pick, the Add button)
          <div className="row" style={{ gap: 8, position: "relative", flex: "1 1 100%" }} onKeyDown={(e) => { if (e.key === "Escape") (e.preventDefault(), close()); }} onBlur={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && (setOpen(false), setAt(-1))}>
            {/* an own list, not a datalist: the browser's datalist popup is its own chrome and opened off the screen
                (review item B); .lang-opts (shell.css) is left-anchored under the field and never wider than the screen */}
            <input ref={box} id="lang-add" className="input" aria-label="Add a language" autoComplete="off" role="combobox" aria-expanded={open} aria-controls="lang-opts" aria-autocomplete="list" aria-describedby="langs-h" aria-activedescendant={at >= 0 ? "lang-o" + at : undefined} value={typed}
              onChange={(e) => (setTyped(e.target.value), setAt(-1), setOpen(true))}
              onFocus={() => setOpen(true)} onClick={() => setOpen(true)}
              onKeyDown={(e) => {
                // a pick from the list adds at once, as the old page did; typed text waits for Add or Enter
                if (e.key === "ArrowDown" && hits.length) return (e.preventDefault(), setOpen(true), setAt((a) => Math.min(a + 1, hits.length - 1)));
                if (e.key === "ArrowUp" && hits.length) return (e.preventDefault(), setAt((a) => Math.max(a - 1, -1)));
                if (e.key === "Enter") return (e.preventDefault(), at >= 0 ? pick(hits[at]) : add());
              }} style={{ flex: 1, minWidth: 0, width: 200, maxWidth: "100%" }} />
            <button className="btn" type="button" onClick={() => add()}><Icon id="i-plus" />Add</button>
            {open && !!hits.length && <div className="pop lang-opts" id="lang-opts" role="listbox" aria-label="Languages">{hits.map((l, i) =>
              <button key={l[0]} id={"lang-o" + i} type="button" role="option" aria-selected={at === i} className={"it" + (at === i ? " on" : "")}
                onMouseDown={(e) => e.preventDefault()} onMouseEnter={() => setAt(i)} onClick={() => pick(l)}>
                <span>{l[1]}</span>{l[2] && l[2] !== l[1] && <span className="muted">{l[2]}</span>}
              </button>)}</div>}
          </div>
        ) : (
          <div><button ref={addBtn} className="btn" type="button" onClick={() => setAdding(true)}><Icon id="i-plus" />Add</button></div>
        )}
      </div>
    </SettingRow>);
}

// Sign-in (design: "Sign-in" section, #sign-in, between General and Appearance): the same choice as setup, from GET /api/auth.
// Save changes the mode or the password; the caller keeps its cookie (the server sets a fresh one), so nothing navigates.
export function SignInSettings() {
  const au = useData(getAuth, []);
  return (
    <section id="sign-in" aria-labelledby="signin-h" className="stack settings-sec">
      <section className="card"><div className="hd"><h2 id="signin-h">Sign-in</h2></div>
        <div className="bd" style={{ padding: 0 }}>
          {au.data ? <SignInForm cur={au.data} saved={au.reload} /> : <div className="empty">{au.error?.message ?? "Loading…"}</div>}
        </div></section>
    </section>);
}

function SignInForm({ cur, saved }: { cur: AuthState; saved: () => void }) {
  const [mode, setMode] = useState<AuthMode>(cur.mode), [pw, setPw] = useState(""), [again, setAgain] = useState(""), [changing, setChanging] = useState(false);
  const [said, setSaid] = useState(""), [bad, setBad] = useState(false), [busy, setBusy] = useState(false);
  const want = { mode, pw, again };
  const newPw = pw !== "" || again !== "";
  // the password fields show when the mode is password and either none is set yet (optional) or Change password was pressed
  const showFields = mode === "password" && (!cur.passwordSet || changing);
  const ok = canSave(cur, want) && (!(mode === "password" && changing) || passwordProblem(pw, again) === null);
  const dirty = mode !== cur.mode || (mode === "password" && newPw);
  const save = async () => {
    setBad(false); setSaid(""); setBusy(true);
    const body = mode === "home" ? { mode } : newPw ? { mode, password: pw } : { mode };
    const r = await setAuthMode(body).catch((e) => { if (e instanceof SignedOut) return null; throw e; }).finally(() => setBusy(false));
    if (!r) return;
    if (failed(r)) return (setBad(true), setSaid(authWord(r.error.code, r.error.message)));
    setPw(""); setAgain(""); setChanging(false);
    setSaid(mode === "home" ? "Saved. Anyone on your home network can open it now without signing in." : "Saved. This browser stays signed in.");
    saved();
  };
  return (<>
    <div className="bd stack" style={{ gap: 12, padding: 16 }}>
      <ModeChoice mode={mode} hideLegend addressesHidden={cur.addressesHidden} onMode={(m) => (setMode(m), setBad(false), setSaid(""))}>
        {mode === "password" && cur.passwordSet && !changing && (
          <div className="row" style={{ marginTop: 8 }}><button className="btn sm" type="button" onClick={() => (setChanging(true), setBad(false), setSaid(""))}>Change password</button></div>
        )}
        {mode === "password" && !cur.passwordSet && <p className="note" style={{ marginTop: 8 }}>No password yet: you sign in with the access token. Set a password to sign in with it instead.</p>}
        {showFields && <PasswordFields pw={pw} again={again} onPw={(v) => (setPw(v), setBad(false))} onAgain={(v) => (setAgain(v), setBad(false))} />}
      </ModeChoice>
      <div className="stack" style={{ gap: 8 }}>
        <p className={bad ? "st bad" : "muted"} role={bad ? "alert" : "status"} style={{ margin: 0 }}>{said}</p>
        <div className="row" style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 12 }}>
          <button className="btn primary" type="button" disabled={!ok || !dirty || busy} onClick={save}>{changing ? "Save password" : "Save sign-in"}</button>
          {changing && <button className="btn ghost" type="button" onClick={() => (setChanging(false), setPw(""), setAgain(""), setBad(false), setSaid(""))}>Cancel</button>}
        </div>
      </div>
    </div>
  </>);
}

// Access: the token only when asked for; a new one asks first; Sign out only when sign-in is by password (design: the token is the sign-in otherwise).
function Access({ auth }: { auth: boolean }) {
  const [tok, setTok] = useState<string | null>(null), [ask, setAsk] = useState(false), [said, setSaid] = useState("");
  const mode = useData(getAuth, []);
  // reload rethrows non-SignedOut errors; nothing to show here
  useEffect(() => onAuthChanged(() => { mode.reload().catch(() => {}); }), [mode.reload]);
  const show = async () => { if (tok) return setTok(null); const r = await act(getToken(), setSaid); if (r) setTok(r.token); };
  const make = async () => { setAsk(false); const r = await act(rotateToken(), setSaid); if (r) (setTok(r.token), setSaid("New token made. Use it to sign in on other devices.")); };
  const out = async () => { if (await act(signOut(), setSaid)) navigate("/signin"); };
  return (<>
    <SettingRow title={<label htmlFor="tok">Access token</label>} desc={<span id="tok-h">{accessTokenWords(mode.data?.mode, !!mode.data?.addressesHidden)}</span>}>
      <input id="tok" className="input mono" readOnly value={tok ?? "••••••••••••"} aria-describedby="tok-h" style={{ width: 200, maxWidth: "100%", minWidth: 0 }} />
      <button className="btn" type="button" aria-controls="tok" onClick={show}>{tok ? "Hide" : "Show"}</button>
      {tok && navigator.clipboard && <button className="btn" type="button" onClick={() => navigator.clipboard.writeText(tok).then(() => setSaid("Copied."), () => {})}>Copy</button>}
      <button className="btn" type="button" onClick={() => setAsk(true)}>Make a new one</button>
    </SettingRow>
    {ask && <div className="note" role="alertdialog" aria-labelledby="tok-ask" style={{ margin: "0 16px 12px" }}><Icon id="i-alert" /><div className="sp"><strong id="tok-ask">Make a new token?</strong> {auth ? "The old one stops working, so other devices must use the new one." : "This server doesn't ask for one now. Making one turns sign-in on for every browser and script, and they will need the token from then on."}</div>
      <button className="btn sm primary" type="button" onClick={make}>Make it</button><button className="btn sm ghost" type="button" onClick={() => setAsk(false)}>Cancel</button></div>}
    <p className="muted" role="status" style={{ margin: said ? "0 16px 12px" : 0 }}>{said || null}</p>
    {auth && mode.data?.mode === "password" && <SettingRow title="Sign out of this browser" desc="You'll need the password to open the app in this browser again.">
      <button className="btn" type="button" aria-label="Sign out of this browser" onClick={out}>Sign out</button>
    </SettingRow>}
  </>);
}

export function SafetySettings() {
  const st = useData(getSettings, []), tr = useData(getTrash, [], 60000);
  const [days, setDays] = useState<string | null>(null), [said, setSaid] = useState(""), [bad, setBad] = useState(false);
  const now = days ?? String(st.data?.keepOriginalsDays ?? ""), n = Number(now), okDays = now.trim() !== "" && Number.isInteger(n) && n >= 0 && n <= 365;
  const save = async () => {
    setBad(false);
    if (!okDays) return (setBad(true), setSaid("Enter a whole number of days, from 0 to 365."));
    if (await act(patchSettings({ keepOriginalsDays: n }), (m) => (setBad(true), setSaid(m)))) (setSaid("Saved."), setDays(null), st.reload());
  };
  // Empty trash now: deletes the kept originals before their keep time is up (a confirm first; the result lists any library skipped)
  const [ask, setAsk] = useState(false), [purging, setPurging] = useState(false), [gone, setGone] = useState<PurgeResult | null>(null), [purgeSaid, setPurgeSaid] = useState(""), purgeBtn = useRef<HTMLButtonElement>(null), freed = useRef<HTMLParagraphElement>(null);
  // U-B: the kept originals the purge couldn't delete; Forget removes a record whose file is gone (a 409 keeps the row and says why)
  const [unreach, setUnreach] = useState<Unreachable[] | null>(null), [rowSaid, setRowSaid] = useState<Record<number, string>>({}), [forgetting, setForgetting] = useState<number | null>(null), [forgotSaid, setForgotSaid] = useState(""), [focusAt, setFocusAt] = useState<number | null>(null), unreachLine = useRef<HTMLParagraphElement>(null);
  // Focus moves after the commit: to the result after a purge, back to the (enabled again) button after an error
  const [back, setBack] = useState(0);
  useEffect(() => { if (gone) freed.current?.focus(); }, [gone]);
  useEffect(() => { if (back) purgeBtn.current?.focus(); }, [back]);
  // a failed Forget: the focused button was disabled while the request ran, so focus goes back to its row after the commit
  const [retry, setRetry] = useState<{ id: number } | null>(null);
  useEffect(() => { if (retry) document.getElementById("forget-" + retry.id)?.focus(); }, [retry]);
  const purge = async () => {
    setPurging(true); setPurgeSaid(""); setGone(null); setUnreach(null); setForgotSaid(""); setRowSaid({});
    const r = await act(purgeTrash(), setPurgeSaid);
    setPurging(false); setAsk(false);
    if (r) (setGone(r), setUnreach(r.unreachable?.length ? r.unreachable : null), tr.reload());
    else setBack((b) => b + 1);
  };
  const forget = async (u: Unreachable, idx: number) => {
    setForgetting(u.id); setRowSaid((m) => ({ ...m, [u.id]: "" }));
    let r: Answer<unknown>;
    try { r = await forgetOriginal(u.id); } catch (e) { if (e instanceof SignedOut) return setForgetting(null); throw e; }
    setForgetting(null);
    if (failed(r)) return (setRowSaid((m) => ({ ...m, [u.id]: r.error.message })), setRetry({ id: u.id }));
    setUnreach((list) => (list ?? []).filter((x) => x.id !== u.id));
    setForgotSaid(`Forgot ${fileName(u.path)}. The file was not deleted.`);
    setFocusAt(idx); tr.reload();
  };
  // after a forget, focus goes to the next Forget button, or to the warning line when none are left
  useEffect(() => {
    if (focusAt === null) return;
    setFocusAt(null);
    const id = nextForgetId(unreach ?? [], focusAt);
    if (id !== null) document.getElementById("forget-" + id)?.focus(); else unreachLine.current?.focus();
  }, [focusAt]); // eslint-disable-line react-hooks/exhaustive-deps
  const t = tr.data;
  return (
    <section id="safety" aria-labelledby="keep-h" className="stack settings-sec">
      {!st.data ? <div className="empty">{st.error?.message ?? "Loading…"}</div> : <>
        <section className="card" aria-labelledby="keep-h"><div className="hd"><h2 id="keep-h">Keeping originals</h2></div>
          <div className="bd stack" style={{ padding: 0, gap: 0 }}>
            <SettingRow title={<label id="keep-days-l" htmlFor="keep-days">Keep originals for</label>} descId="keep-h2" desc="0 means the original is deleted right after the new file has been checked.">
              <div className="row nw" style={{ gap: 8 }}>
                <input id="keep-days" className="input" type="number" inputMode="numeric" min={0} max={365} step={1} value={now} onChange={(e) => setDays(e.target.value)} aria-labelledby="keep-days-l keep-days-u" aria-describedby="keep-h2" style={{ width: 140, maxWidth: "100%" }} />
                <span id="keep-days-u" className="muted">days</span>
              </div>
            </SettingRow>
            {/* the server's trashPath is a sentence ("... in a folder called .syp-trash"); the folder name is taken from it */}
            {st.data.trashPath && <SettingRow title="Where originals are kept" desc="Next to each file, in a folder of their own.">
              <span className="mono">{st.data.trashPath.match(/called (\S+)/)?.[1] ?? st.data.trashPath}</span>
            </SettingRow>}
            <SettingRow title="Kept right now" descId="purge-h" desc={<span aria-live="polite">{!t ? (tr.error?.message ?? "") : t.files ? <><strong>Kept originals:</strong> {bytes(t.bytes)} · {plural(t.files, "file", "files")}.{t.nextDeleteAt && " The next ones go on " + day(t.nextDeleteAt) + "."}</> : "No originals are being kept right now."}</span>}>
              {t && <button ref={purgeBtn} className="btn" type="button" disabled={!t.files || purging} aria-describedby={t.files ? undefined : "purge-h"} onClick={() => (setGone(null), setUnreach(null), setPurgeSaid(""), setAsk(true))}>
                {t.files ? `Empty trash now: ${plural(t.files, "file", "files")}, ${bytes(t.bytes)}` : "Empty trash now"}</button>}
            </SettingRow>
            {purgeSaid && <p className="st bad" role="alert" style={{ margin: "0 16px 12px" }}><Icon id="i-alert" /><span>{purgeSaid}</span></p>}
            {gone && <div aria-live="polite" className="stack" style={{ gap: 6, padding: "0 16px 12px" }}>
              <p ref={freed} tabIndex={-1} className={gone.deletedFiles ? "st ok" : "st warn"} style={{ margin: 0 }}><Icon id={gone.deletedFiles ? "i-check-c" : "i-alert"} /><span>{gone.deletedFiles ? `Freed ${bytes(gone.freedBytes)} (${plural(gone.deletedFiles, "file", "files")}).` : "Nothing was deleted."}</span></p>
              {gone.skipped.map((s, i) => <p key={i} className="st warn" style={{ margin: 0 }}><Icon id="i-alert" /><span>{`${s.library} was skipped. ${s.reason}`}</span></p>)}
              {forgotSaid && <p role="status" className="st" style={{ margin: 0 }}><span>{forgotSaid}</span></p>}
              {unreach && <div className="stack" style={{ gap: 8 }}>
                <p ref={unreachLine} tabIndex={-1} className="st warn" style={{ margin: 0 }}><Icon id="i-alert" /><span>{unreach.length ? unreachableLine(unreach.length) : "Every original that couldn't be deleted has been forgotten."}</span></p>
                {unreach.length > 0 && <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gap: 8 }}>
                  {unreach.map((u, i) => <li key={u.id} style={{ display: "flex", flexWrap: "wrap", gap: "8px 11px", alignItems: "center", justifyContent: "space-between", borderTop: "1px solid var(--line, #ddd)", paddingTop: 8 }}>
                    <div style={{ minWidth: 0, flex: "1 1 220px" }}>
                      <div className="mono" style={{ overflowWrap: "anywhere" }}>{u.path}</div>
                      <div className="muted" style={{ overflowWrap: "anywhere" }}>{reasonLine(u.reason)}</div>
                      {rowSaid[u.id] && <p className="st bad" role="alert" style={{ margin: "4px 0 0" }}><Icon id="i-alert" /><span>{rowSaid[u.id]}</span></p>}
                    </div>
                    {canForget(u) && <button id={"forget-" + u.id} className="btn" type="button" disabled={forgetting !== null} aria-label={`Forget ${fileName(u.path)}`} onClick={() => forget(u, i)}>Forget</button>}
                  </li>)}
                </ul>}
              </div>}
            </div>}
          </div>
        </section>
        <Commands settings={st} />
        {/* one Save for this tab: it saves the days only; Empty trash now and the switch above apply at once */}
        <div className="stack" style={{ gap: 8 }}>
          <p className={bad ? "st bad" : "muted"} role="status" style={{ margin: 0 }}>{said}</p>
          <div className="row" style={{ justifyContent: "flex-start", flexWrap: "wrap", gap: 12 }}>
            <button className="btn primary" type="button" aria-label="Save safety settings" disabled={days == null || Number(days) === st.data.keepOriginalsDays} onClick={save}>Save</button>
            <span className="muted">Saves the number of days only. Empty trash now applies at once.</span>
          </div>
        </div>
      </>}
      {ask && t && <Confirm title="Empty trash now?" body={`This deletes ${plural(t.files, "original file", "original files")} (${bytes(t.bytes)}), kept in case you need them back. It can't be undone.`}
        yes={purging ? "Deleting…" : "Delete " + plural(t.files, "file", "files")} no="Cancel" danger busy={purging} onYes={purge} onNo={() => (setAsk(false), purgeBtn.current?.focus())} />}
    </section>);
}

// Workflows may run commands (flow builder FB2b, the user's decision): the switch behind a workflow's "Run a command" step. Off
// by default; off, such a workflow can't be published and a published one's step reports that it can't run.
function Commands({ settings }: { settings: ReturnType<typeof useData<FirstRunSettings>> }) {
  const [busy, setBusy] = useState(false), [said, setSaid] = useState("");
  const on = !!settings.data?.workflowsMayRunCommands;
  const set = async (v: boolean) => { setBusy(true); setSaid(""); const r = await act(patchSettings({ workflowsMayRunCommands: v }), setSaid); setBusy(false); if (r) settings.reload(); };
  return (
    <section className="card" aria-labelledby="cmd-h"><div className="hd"><h2 id="cmd-h">Workflows and programs</h2></div>
      <div className="bd stack" style={{ padding: 0, gap: 0 }}>
        <SettingRow title={<label htmlFor="cmd-sw">Workflows may run commands</label>} descId="cmd-hint" desc={`A workflow's "Run a command" step starts a program of yours after a file is saved, with the file's path and variables as arguments. The program runs as this app's user, with everything it can reach, so this stays off until you turn it on.`}>
          <input id="cmd-sw" type="checkbox" role="switch" className="switch" checked={on} disabled={busy} aria-describedby="cmd-hint" onChange={(e) => set(e.target.checked)} />
          <span>{on ? "On" : "Off"}</span>
        </SettingRow>
        {!on && <div className="note" style={{ margin: "0 16px 12px" }}><Icon id="i-shield" /><span>Off: a workflow with a "Run a command" step can't be published, and a published one's step says it can't run.</span></div>}
        {said && <p className="st bad" role="alert" style={{ margin: "0 16px 12px" }}><Icon id="i-alert" /><span>{said}</span></p>}
      </div></section>);
}

// Updates (SCREENS:293, About block): the #324 switch, off until the person turns it on, and when it last checked.
const SENT = "Once a day it asks GitHub which stable release is newest. GitHub sees this app's version and your IP address, as with any web request. Nothing about your files or settings is sent. Checking installs nothing.";
const at = (t: string) => `${day(t)}, ${new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
function Updates() {
  const st = useData(getSettings, []), u = useData(getUpdate, []), [busy, setBusy] = useState(false), [said, setSaid] = useState("");
  const on = !!st.data?.checkForUpdates, d = u.data, sw = useRef<HTMLInputElement>(null);
  const set = async (v: boolean) => { setBusy(true); setSaid(""); const r = await act(patchSettings({ checkForUpdates: v }), setSaid); setBusy(false); if (r) { st.reload(); u.reload(); } };
  const now = async () => { setBusy(true); setSaid(""); const r = await act(checkUpdate(), setSaid); setBusy(false); if (r) u.reload(); };
  const toSwitch = (e: MouseEvent) => { e.preventDefault(); sw.current?.scrollIntoView({ block: "center" }); sw.current?.focus(); };
  return (
    <section className="card" aria-labelledby="upd-h"><div className="hd"><h2 id="upd-h">Updates</h2></div>
      <div className="bd stack" style={{ padding: 0, gap: 0 }}>
        <SettingRow title={<label htmlFor="upd-sw">Check for updates</label>} descId="upd-hint" desc={SENT}>
          <input ref={sw} id="upd-sw" type="checkbox" role="switch" className="switch" checked={on} disabled={!st.data || busy} aria-describedby="upd-hint" onChange={(e) => set(e.target.checked)} />
          <span>{on ? "On" : "Off"}</span>
        </SettingRow>
        <div className="stack" style={{ padding: "0 var(--pad) 12px" }}>
        <div className="note"><Icon id="i-shield" /><span>The update check is the only thing Shrink Your Shit sends over the internet without you setting it up.</span></div>
        {!d ? u.error && <p role="status" style={{ margin: 0 }}><span className="muted">{u.error.message}</span></p>
          : <div className="row" style={{ alignItems: "baseline" }}>
          <p role="status" style={{ margin: 0 }}>
            {!d.enabled ? <span className="muted">Update checks are off. <a href="#upd-sw" onClick={toSwitch}>Turn them on</a></span>
              : d.available && d.latest ? <><span className="pill warn"><Icon id="i-download" />Version {d.latest} is available</span>{d.url && <> <a href={d.url} target="_blank" rel="noopener">What's new</a></>}</>
                : d.error && d.checkedAt ? <span className="st warn"><Icon id="i-alert" /><span>Couldn't check for updates at {at(d.checkedAt)}.</span></span>
                  : d.checkedAt ? <span className="muted">Up to date. Checked {at(d.checkedAt)}.</span> : <span className="muted">Not checked yet.</span>}</p>
          {d.enabled && d.install.state !== "downloading" && d.install.state !== "installing" && <button className="btn sm" type="button" disabled={busy} onClick={now}><Icon id="i-refresh" />{busy ? "Checking…" : "Check now"}</button>}
        </div>}
        {said && <p className="st bad" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{said}</span></p>}
        {d && <Install d={d} reload={u.reload} check={now} />}
        </div>
      </div></section>);
}

// The update button (SCREENS:301, updater part 2, contract ## Update). Which block shows follows method, install.state and
// lastResult. While an install runs the card re-reads every 3 s; when the server is gone (it restarts) it says so.
// Roll back (#336) shows only when the helper keeps a previous version, and works with update checks off.
const REFUSED: Record<string, string> = { job_running: "A file just started converting.", not_available: "There's no newer version now.",
  updates_off: "Update checks are off. Turn them on to update.", update_in_progress: "", install_unsupported: "", no_previous: "There's no earlier version kept to go back to." };
// design #456: our words for the cancel 409s; the card reloads after, since the state has moved on
const CANCEL_REFUSED: Record<string, string> = { update_in_progress: "The update has already started, so it can't be cancelled now.", nothing_to_cancel: "There's no update waiting to start." };
const noV = (m: string) => m.replace(/\bv(?=\d)/g, ""); // design gap d: versions read as on the Version line
// plain http has no clipboard API (the user's http://IP:8484), or it can be refused: then copy the old way, as Workers does
async function copyText(t: string): Promise<boolean> {
  try { if (navigator.clipboard) { await navigator.clipboard.writeText(t); return true; } } catch { /* refused: fall back */ }
  const a = document.createElement("textarea"); a.value = t; a.setAttribute("readonly", ""); a.style.position = "fixed"; a.style.opacity = "0";
  document.body.appendChild(a); a.select(); const ok = document.execCommand("copy"); a.remove(); return ok;
}
function Copy({ t }: { t: string }) {
  const [said, setSaid] = useState("");
  return (<><div className="codebox"><code>{t}</code><button className="btn sm" type="button" onClick={async () => setSaid((await copyText(t)) ? "Copied." : "Select the command and copy it.")}><Icon id="i-copy" />Copy</button></div>
    {said && <span className="hint" role="status">{said}</span>}</>);
}
function Install({ d, reload, check }: { d: UpdateNotice; reload: () => void; check: () => void }) {
  const jobs = useData(getConverting, []), [busy, setBusy] = useState(false), [said, setSaid] = useState(""), [gone, setGone] = useState(false), [late, setLate] = useState(false), [noNew, setNoNew] = useState(false), [rbFail, setRbFail] = useState("");
  // rolling: what the page saw when Roll back was accepted; it waits until the version or the last result changes
  const [ask, setAsk] = useState(false), [rolling, setRolling] = useState<{ from: string; msg: string } | null>(null);
  // cancelling a waiting update: the confirm, the result line, and the button the dialog gives focus back to
  const [cancelAsk, setCancelAsk] = useState(false), [cancelled, setCancelled] = useState(false), cancelBtn = useRef<HTMLButtonElement>(null);
  const st = d.install.state, running = st === "waiting" || st === "downloading" || st === "installing", n = jobs.data ?? 0, to = d.latest ?? "", last = d.lastResult;
  useEffect(() => { if (rolling && (d.current !== rolling.from || (d.lastResult?.message ?? "") !== rolling.msg)) setRolling(null); }, [d, rolling]);
  useEffect(() => setRbFail(""), [d.current]); // a late restart clears an old roll back failure
  // a helper that accepts but never restarts the app would leave "Restarting..." up forever
  useEffect(() => { if (!rolling) return; const t = setTimeout(() => { setRolling(null); setRbFail(`still on ${rolling.from} after 3 minutes. Try again, or check the server's log.`); }, 180000); return () => clearTimeout(t); }, [rolling]);
  useEffect(() => {
    if (!running && !gone && !rolling) return;
    const t = setInterval(async () => { const r = await getUpdate().catch(() => null); if (!r || failed(r)) setGone(r?.error.code === "offline" || !r); else { setGone(false); reload(); } }, 3000);
    return () => clearInterval(t);
  }, [running, gone, rolling, reload]);
  const cancel = async () => {
    setBusy(true); setSaid(""); setNoNew(false); setRbFail(""); setCancelled(false);
    try { const r = await cancelUpdate(); if (!failed(r)) setCancelled(true); else setSaid(CANCEL_REFUSED[r.error.code] ?? r.error.message); }
    catch (e) { if (!(e instanceof SignedOut)) throw e; }
    setBusy(false); setCancelAsk(false); reload();
  };
  const go = async (when: "now" | "after-job") => {
    setBusy(true); setSaid(""); setNoNew(false); setRbFail(""); setCancelled(false);
    try { const r = await installUpdate(when); if (!failed(r)) holdLost(120000); else { setNoNew(r.error.code === "not_available"); setSaid(r.error.code in REFUSED ? REFUSED[r.error.code] : r.error.message); if (r.error.code === "job_running") setLate(true); jobs.reload(); } }
    catch (e) { if (!(e instanceof SignedOut)) throw e; }
    setBusy(false); reload();
  };
  const back = async () => {
    setBusy(true); setSaid(""); setNoNew(false); setRbFail("");
    try { const r = await rollbackUpdate(); if (!failed(r)) { holdLost(120000); setRolling({ from: d.current, msg: d.lastResult?.message ?? "" }); } else if (r.error.code in REFUSED) setSaid(REFUSED[r.error.code]); else setRbFail(/[.!?]$/.test(r.error.message) ? r.error.message : r.error.message + "."); }
    catch (e) { if (!(e instanceof SignedOut)) throw e; }
    setBusy(false); setAsk(false); reload();
  };
  const prev = d.rollback.version, prevName = prev ?? "the previous version", wait = late || n > 0, retry = !!last && !last.ok && last.to === to;
  const spin = (t: string, rest = "") => <p className="st run" role="status" style={{ margin: 0, alignItems: "flex-start" }}><span className="spin" style={{ display: "inline-flex", marginTop: 2 }}><Icon id="i-refresh" /></span><span>{t}{rest && <span style={{ fontWeight: 400, color: "var(--text)" }}> {rest}</span>}</span></p>;
  return (<>
    {last && !running && !gone && !rolling && (last.ok
      ? <p className="st ok" style={{ margin: 0 }}><Icon id="i-check-c" /><span>{noV(last.message)}.{d.url && !last.rolledBack && <> <a href={d.url} target="_blank" rel="noopener">What's new</a></>}</span></p>
      : <p className="st warn" style={{ margin: 0, alignItems: "flex-start" }}><Icon id="i-alert" /><span className="why" style={{ fontWeight: 400, color: "var(--text)" }}>{noV(last.message)}.</span></p>)}
    {gone || rolling ? spin("Restarting…", "this page reconnects by itself.")
      : st === "waiting" ? <div className="stack" style={{ gap: 6 }}><p role="status" style={{ margin: 0 }}>Updating to {to} when the converting files finish.</p>
        <div><button ref={cancelBtn} className="btn" type="button" disabled={busy} onClick={() => setCancelAsk(true)}>Cancel update</button></div></div>
      : st === "downloading" ? spin(`Downloading ${to}…`)
      : st === "installing" ? spin(`Installing ${to}.`, "The app restarts, and this page reconnects by itself.")
      : st === "failed" ? <p className="st warn" style={{ margin: 0, alignItems: "flex-start" }}><Icon id="i-alert" /><span>Couldn't update to {to}: <span style={{ fontWeight: 400, color: "var(--text)" }}>{d.install.error}. Nothing changed; still on {d.current}.</span></span></p>
      : null}
    {d.available && !running && !gone && !rolling && (d.method === "helper" ? <div className="stack" style={{ gap: 6 }}>
        {wait && n > 0 && <p style={{ margin: 0 }}>{plural(n, "file is", "files are")} converting.</p>}
        <div><button className={"btn " + (retry || st === "failed" ? "" : "primary")} type="button" disabled={busy} onClick={() => go(wait ? "after-job" : "now")}>
          {retry || st === "failed" ? "Try again" : wait ? (n > 1 ? "Update when they finish" : "Update when it finishes") : `Update to ${to}`}</button></div>
        <span className="hint">{wait ? <>Or stop them on <Link to="/jobs">Jobs</Link>, then update.</> : `Takes about a minute. The current version is kept: if ${to} doesn't start properly, it goes back to ${d.current} by itself.`}</span>
      </div>
      : d.method === "commands" && d.commands ? <div className="stack" style={{ gap: 6 }}>
        <p style={{ margin: 0 }}>{d.installType === "docker" ? "This runs in Docker. Update it where your compose file is:" : "To update, run this on the server:"}</p>
        <Copy t={d.commands.join("\n")} />
        {d.helperCommand && <><p style={{ margin: 0 }}>To update from this page instead, run the installer once with the update helper:</p>
          <Copy t={d.helperCommand} />
          <span className="hint">The helper runs as root and only installs releases of this app, checked against their checksums.</span></>}
        {d.os === "darwin" && <span className="hint">Updating on macOS hasn't been tested yet.</span>}
        {d.installType === "docker" && <span className="hint">Or let <a href="https://containrrr.dev/watchtower/" target="_blank" rel="noopener">Watchtower</a> update the container for you.</span>}
      </div>
      : d.installType === "windows" ? <p style={{ margin: 0 }}>Download the new installer from the {d.url ? <a href={d.url} target="_blank" rel="noopener">release page</a> : "release page"}.</p>
      : d.installType === "source" ? <p style={{ margin: 0 }}>Built from source: pull and build again.</p> : null)}
    {rbFail && <p className="st warn" role="alert" style={{ margin: 0, alignItems: "flex-start" }}><Icon id="i-alert" /><span>Couldn't roll back: <span style={{ fontWeight: 400, color: "var(--text)" }}>{rbFail}</span></span></p>}
    {said && <p className="st warn" role="alert" style={{ margin: 0 }}><Icon id="i-alert" /><span>{said}{noNew && <> <button className="linkbtn" type="button" onClick={() => (setSaid(""), setNoNew(false), check())}>Check now</button></>}</span></p>}
    {cancelled && <p className="st ok" role="status" style={{ margin: 0 }}><Icon id="i-check-c" /><span>Update cancelled.</span></p>}
    {cancelAsk && <Confirm title={`Cancel the update to ${to}?`} yes="Cancel update" no="Keep waiting" busy={busy} onYes={cancel}
      onNo={() => (setCancelAsk(false), cancelBtn.current?.focus())} body="Nothing is installed, and workers start again if the update paused them." />}
    {d.method === "helper" && st === "idle" && d.rollback.available && !gone && !rolling &&
      <div><button className="btn" type="button" disabled={busy} onClick={() => setAsk(true)}>{prev ? `Roll back to ${prev}` : "Roll back"}</button></div>}
    {ask && <Confirm title={`Go back to ${prevName}?`} yes="Roll back" no="Cancel" busy={busy} onYes={back} onNo={() => setAsk(false)}
      body={`The app restarts on ${prevName}. ` + (d.rollback.dataRestored ? "Settings and job history go back to how they were before the update. Files converted since then stay as they are." : "Settings and job history stay as they are.")} />}
  </>);
}

// About (SCREENS V9: version, log lines, updates, licences). The health checks have their own tab (Doctor.tsx).
export function AboutSettings() {
  const h = useData(getHealth, []);
  return (
    <section id="about" aria-labelledby="about-h" className="stack settings-sec">
      <section className="card" aria-labelledby="about-h"><div className="hd"><h2 id="about-h">About</h2></div>
        <div className="bd" style={{ padding: 0 }}>
          <SettingRow title="Version">{h.data?.version && <ValueChip>{h.data.version}</ValueChip>}</SettingRow>
          <SettingRow title="Licence" desc={<>Shrink Your Shit is free software under the GNU Affero General Public License, version 3. <a href="/licenses.md" target="_blank" rel="noopener">The licences of the parts this interface is built with</a> (React, React Flow and others).</>} />
        </div></section>
      <Updates />
      <section className="card" aria-labelledby="log-h"><div className="hd"><h2 id="log-h">Recent log lines</h2></div>
        <div className="bd"><pre className="mono sm" tabIndex={0} role="region" aria-labelledby="log-h" style={{ overflow: "auto", maxHeight: "16rem", margin: 0, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{h.data ? (h.data.logsTail.length ? h.data.logsTail.join("\n") : "Nothing has been logged yet.") : ""}</pre></div></section>
    </section>);
}
