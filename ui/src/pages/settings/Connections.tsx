import { useState } from "react";
import { api, type Integration, type IntegrationKind, type PathMapping } from "../../api/index.ts";
import { useApi } from "../../api/useApi.ts";
import { Link } from "../../router.tsx";
import { LoadState, errText } from "./parts.tsx";

const KINDS: { value: IntegrationKind; label: string }[] = [
  { value: "sonarr", label: "Sonarr" },
  { value: "radarr", label: "Radarr" },
  { value: "plex", label: "Plex" },
  { value: "jellyfin", label: "Jellyfin" },
];

// Plain-words result of a test, next to the row's own button.
function TestNote({ text, ok }: { text: string | null; ok: boolean }) {
  if (!text) return null;
  return <span className={ok ? "muted sm" : "bad sm"} role="status">{text}</span>;
}

function ConnRow({ c, onChanged }: { c: Integration; onChanged: () => void }) {
  const [name, setName] = useState(c.name);
  const [url, setUrl] = useState(c.url);
  const [apiKey, setApiKey] = useState("");
  const [maps, setMaps] = useState<PathMapping[]>(c.pathMappings);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const dirty = name !== c.name || url !== c.url || apiKey !== "" || JSON.stringify(maps) !== JSON.stringify(c.pathMappings);

  const save = () => {
    setBusy(true);
    setErr(null);
    api.patchIntegration(c.id, { name, url, ...(apiKey ? { apiKey } : {}), pathMappings: maps }).then(
      () => {
        setApiKey("");
        onChanged();
      },
      (e: unknown) => setErr(errText(e)),
    ).finally(() => setBusy(false));
  };
  const runTest = () => {
    setBusy(true);
    setTest(null);
    api.testIntegration({ id: c.id }).then(
      (r) => setTest({ ok: r.ok, text: r.ok ? `Connected${r.version ? `, version ${r.version}` : ""}.` : r.message }),
      (e: unknown) => setTest({ ok: false, text: errText(e) }),
    ).finally(() => setBusy(false));
  };
  const remove = () => {
    setBusy(true);
    api.deleteIntegration(c.id).then(onChanged, (e: unknown) => setErr(errText(e))).finally(() => setBusy(false));
  };

  return (
    <section className="set-group">
      <div className="set-grouphead">
        <b>{name || c.name}</b> <span className="muted sm">{KINDS.find((k) => k.value === c.kind)?.label}</span>
        {c.failingSince && <span className="bad sm"> · failing</span>}
      </div>
      <div className="set-row">
        <div className="set-lab"><label htmlFor={`c-name-${c.id}`}>Name</label></div>
        <div className="set-ctl"><input id={`c-name-${c.id}`} className="inp" value={name} onChange={(e) => setName(e.target.value)} /></div>
      </div>
      <div className="set-row">
        <div className="set-lab"><label htmlFor={`c-url-${c.id}`}>Address</label><div className="hint">Where this app reaches it.</div></div>
        <div className="set-ctl"><input id={`c-url-${c.id}`} className="inp" value={url} onChange={(e) => setUrl(e.target.value)} /></div>
      </div>
      <div className="set-row">
        <div className="set-lab"><label htmlFor={`c-key-${c.id}`}>API key</label><div className="hint">{c.hasKey ? "A key is saved. Type one to replace it." : "No key saved."}</div></div>
        <div className="set-ctl"><input id={`c-key-${c.id}`} className="inp" type="password" autoComplete="off" value={apiKey} onChange={(e) => setApiKey(e.target.value)} /></div>
      </div>
      <div className="set-row">
        <div className="set-lab"><span>Path mappings</span><div className="hint">Their path on the left, ours on the right.</div></div>
        <div className="set-ctl">
          {maps.map((m, i) => (
            <div className="row" key={i}>
              <input aria-label={`Their path ${i + 1}`} className="inp" value={m.theirPath} onChange={(e) => setMaps(maps.map((x, j) => (j === i ? { ...x, theirPath: e.target.value } : x)))} />
              <input aria-label={`Our path ${i + 1}`} className="inp" value={m.ourPath} onChange={(e) => setMaps(maps.map((x, j) => (j === i ? { ...x, ourPath: e.target.value } : x)))} />
              <button type="button" className="btn ghost sm" onClick={() => setMaps(maps.filter((_, j) => j !== i))}>Remove</button>
            </div>
          ))}
          <button type="button" className="btn ghost sm" onClick={() => setMaps([...maps, { theirPath: "", ourPath: "" }])}>Add mapping</button>
        </div>
      </div>
      <div className="set-row">
        <div className="set-lab"><span>Test</span><div className="hint">Checks the address and key.</div></div>
        <div className="set-ctl row">
          <button type="button" className="btn sm" onClick={runTest} disabled={busy}>Test connection</button>
          <TestNote text={test?.text ?? null} ok={test?.ok ?? true} />
        </div>
      </div>
      {err && <p className="bad" role="alert">{err}</p>}
      <div className="row">
        <button type="button" className="btn primary sm" onClick={save} disabled={busy || !dirty}>Save connection</button>
        <span className="sp" />
        <button type="button" className="btn ghost sm" onClick={remove} disabled={busy}>Delete</button>
      </div>
    </section>
  );
}

function AddConn({ onAdded }: { onAdded: () => void }) {
  const [kind, setKind] = useState<IntegrationKind>("sonarr");
  const [url, setUrl] = useState("");
  const [key, setKey] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const add = () => {
    setBusy(true);
    setErr(null);
    api.createIntegration({ kind, url, apiKey: key || undefined }).then(
      () => {
        setUrl("");
        setKey("");
        onAdded();
      },
      (e: unknown) => setErr(errText(e)),
    ).finally(() => setBusy(false));
  };
  return (
    <section className="set-group">
      <div className="set-grouphead"><b>Add a connection</b></div>
      <div className="set-row">
        <div className="set-lab"><label htmlFor="c-new-kind">Kind</label></div>
        <div className="set-ctl">
          <select id="c-new-kind" className="sel" value={kind} onChange={(e) => setKind(e.target.value as IntegrationKind)}>
            {KINDS.map((k) => <option key={k.value} value={k.value}>{k.label}</option>)}
          </select>
        </div>
      </div>
      <div className="set-row">
        <div className="set-lab"><label htmlFor="c-new-url">Address</label></div>
        <div className="set-ctl"><input id="c-new-url" className="inp" value={url} onChange={(e) => setUrl(e.target.value)} /></div>
      </div>
      <div className="set-row">
        <div className="set-lab"><label htmlFor="c-new-key">API key</label></div>
        <div className="set-ctl"><input id="c-new-key" className="inp" type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} /></div>
      </div>
      {err && <p className="bad" role="alert">{err}</p>}
      <div className="row"><button type="button" className="btn primary sm" onClick={add} disabled={busy || url === ""}>Add connection</button></div>
    </section>
  );
}

export function Connections() {
  const list = useApi(() => api.getIntegrations(), []);
  const data = list.data;
  return (
    <>
      <h2 className="set-title">Connections</h2>
      <p className="muted sm">Media managers and servers this app talks to. Each row saves on its own.</p>
      {!data && <LoadState s={list} />}
      {data?.map((c) => <ConnRow key={c.id} c={c} onChanged={list.reload} />)}
      {data && data.length === 0 && <p className="muted">No connections yet.</p>}
      {data && <AddConn onAdded={list.reload} />}
      <p className="muted sm"><Link to="/settings/health">Check the health of these connections</Link></p>
    </>
  );
}
