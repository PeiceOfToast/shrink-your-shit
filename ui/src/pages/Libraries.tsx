import { navigate, useLocation } from "../router.tsx";
import { api, formatCount, formatGB, formatPct, type Library } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { libraryStatus } from "../ui/libraryStatus.ts";
import { AddLibrary } from "./AddLibrary.tsx";
import { LibraryPanel } from "./LibraryPanel.tsx";

// Built as draft-b/libraries.html. A row click (or Enter) opens the library panel; ?add=1 opens Add library.

function LibraryRow({ l }: { l: Library }) {
  const open = () => navigate(`/libraries/${l.id}`);
  const st = libraryStatus(l);
  return (
    <tr className="row-link" tabIndex={0} onClick={open} onKeyDown={(e) => e.key === "Enter" && open()}>
      <td className="full">
        <b>{l.name}</b>
        <br />
        <span className="muted sm" style={{ fontFamily: "var(--mono)" }}>{l.path}</span>
      </td>
      <td data-l="Workflow">{l.workflow ? l.workflow.name : <span className="warn">None yet · pick one</span>}</td>
      <td className="num tnum" data-l="Files">{formatCount(l.files)}</td>
      <td className="num tnum" data-l="Waiting">{formatCount(l.waiting)}</td>
      <td className="num tnum" data-l="Saved">
        {l.savedBytes > 0 ? `${formatGB(l.savedBytes / 1e9)} · ${formatPct(l.savedPct)}` : "—"}
      </td>
      <td data-l="Last scan">{l.lastScan ? l.lastScan.at : "Never"}</td>
      <td>
        <span className={`pill ${st.kind}`}>{st.label}</span>
      </td>
    </tr>
  );
}

export function Libraries({ selected }: { selected?: string }) {
  const { query } = useLocation();
  const adding = query.get("add") === "1";
  // Reload when the panel or the add form opens or closes, so a library added or removed shows at once.
  const libs = useApi(() => api.getLibraries(), [selected, adding]);

  return (
    <>
      <div className="pagehead">
        <h1>Libraries</h1>
        <span className="muted sm">The folders that are watched for videos.</span>
        <span className="sp"></span>
        <button type="button" className="btn primary sm" onClick={() => navigate("/libraries?add=1")}>
          Add library
        </button>
      </div>

      <section className="card" style={{ padding: 0 }}>
        {libs.error && !libs.data && (
          <p className="bad pad">
            Can't load libraries.{" "}
            <button type="button" className="btn ghost sm" onClick={libs.reload}>Try now</button>
          </p>
        )}
        {libs.loading && !libs.data && (
          <table className="tbl" aria-busy="true">
            <tbody>
              {[0, 1, 2].map((i) => (
                <tr key={i}>
                  <td colSpan={7}>
                    <span className="skel" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {libs.data && libs.data.length === 0 && (
          <p className="muted pad">
            No libraries yet. Add the folder with your videos.{" "}
            <button type="button" className="btn primary sm" onClick={() => navigate("/libraries?add=1")}>
              Add library
            </button>
          </p>
        )}
        {libs.data && libs.data.length > 0 && (
          <table className="tbl">
            <thead>
              <tr>
                <th>Library</th>
                <th>Workflow</th>
                <th className="num">Files</th>
                <th className="num">Waiting</th>
                <th className="num">Saved</th>
                <th>Last scan</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {libs.data.map((l) => (
                <LibraryRow key={l.id} l={l} />
              ))}
            </tbody>
          </table>
        )}
      </section>

      {adding && <AddLibrary onClose={() => navigate("/libraries")} onAdded={(l) => navigate(`/libraries/${l.id}`)} />}
      {selected && <LibraryPanel key={selected} id={selected} />}
    </>
  );
}
