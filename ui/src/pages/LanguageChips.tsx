import { useState } from "react";
import { api } from "../api/index.ts";
import { useApi } from "../api/useApi.ts";
import { languageName, normalizeLanguage } from "../format/language.ts";

// The two choices the list from the API doesn't include. Their codes are final: orig and und.
// Only the flow editor's audio track offers them; General's "My languages" does not.
const SPECIAL_LANGUAGES = [
  { code: "orig", name: "Original language" },
  { code: "und", name: "Unknown" },
];

// Languages as chips: a picked one is removed by its chip; "+ Add" opens a search over the rest. No free text.
// Renders the chips and the search only; the caller puts it in its own row.
export function LanguageChips({
  label,
  value,
  onChange,
  offerSpecial = true,
}: {
  label: string;
  value: string[];
  onChange: (v: string[]) => void;
  offerSpecial?: boolean;
}) {
  const all = useApi(() => api.getLanguages(), []);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const picked = value.map(normalizeLanguage);
  const special = offerSpecial ? SPECIAL_LANGUAGES : [];
  const options = [...special, ...(all.data ?? [])].filter((l) => !picked.includes(l.code));
  const needle = query.trim().toLowerCase();
  const shown = needle ? options.filter((l) => l.name.toLowerCase().includes(needle)) : options;
  const close = () => {
    setOpen(false);
    setQuery("");
  };
  return (
    <>
      <div className="wf-togs" role="group" aria-label={label}>
        {picked.map((code) => (
          <button key={code} type="button" aria-label={`Remove ${languageName(code)}`} onClick={() => onChange(picked.filter((c) => c !== code))}>
            {languageName(code)} ×
          </button>
        ))}
        <button type="button" className="btn sm" aria-expanded={open} onClick={() => (open ? close() : setOpen(true))}>
          + Add
        </button>
      </div>
      {open && (
        <div className="wf-lang-search" onKeyDown={(e) => e.key === "Escape" && close()}>
          <input className="inp" type="search" aria-label="Search languages" placeholder="Search languages" autoFocus value={query} onChange={(e) => setQuery(e.target.value)} />
          {shown.length === 0 ? (
            <p className="hint">No language matches “{query.trim()}”.</p>
          ) : (
            <ul className="wf-lang-list">
              {shown.map((l) => (
                <li key={l.code}>
                  <button
                    type="button"
                    onClick={() => {
                      onChange([...picked, l.code]);
                      close();
                    }}
                  >
                    {l.name}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </>
  );
}
