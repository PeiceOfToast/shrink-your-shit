// Live updates: GET /api/v2/events (SSE, #118/#122). One stream for the app, open while anything listens. It keeps the
// newest seq it has seen (hello/resync and each event's id) and, when the stream drops, reconnects with ?since=<seq>
// (the server replays what was missed, or sends resync: reload). After 3 s without a stream the app shows the
// connection-lost bar (spec 12). With ?mock=1 there is no stream.
import { useSyncExternalStore } from "react";
import { MOCK } from "./api";

type Listener = (type: string, data: unknown) => void;
const listeners = new Set<Listener>(), watchers = new Set<() => void>();
let es: EventSource | null = null, seq: number | null = null, retry: ReturnType<typeof setTimeout> | undefined, lostTimer: ReturnType<typeof setTimeout> | undefined;
let state = { lost: false, lastAt: null as Date | null }, tries = 0;
const set = (s: Partial<typeof state>) => { state = { ...state, ...s }; watchers.forEach((w) => w()); };
const emit = (type: string, data: unknown) => listeners.forEach((l) => l(type, data));
const TYPES = ["hello", "resync", "job.state", "job.progress", "processing.paused", "processing.resumed", "activity", "worker.heartbeat", "library.changed", "workers.changed", "setup.changed"]; // library.changed: #150. A name missing here never reaches onLive (EventSource drops named events nobody subscribed to).

function open() {
  if (MOCK || es) return;
  es = new EventSource("/api/v2/events" + (seq != null ? "?since=" + seq : ""));
  es.onopen = () => { tries = 0; clearTimeout(lostTimer); set({ lost: false }); };
  for (const t of TYPES) es.addEventListener(t, (e) => {
    const m = e as MessageEvent, data = JSON.parse(m.data || "null") as { seq?: number } | null;
    if (m.lastEventId) seq = Number(m.lastEventId); else if ((t === "hello" || t === "resync") && typeof data?.seq === "number") seq = data.seq;
    set({ lastAt: new Date() });
    emit(t, data);
  });
  es.onerror = () => { // our own reconnect, with ?since, so nothing between hello and the first event is lost
    es?.close(); es = null;
    clearTimeout(lostTimer); lostTimer = setTimeout(() => set({ lost: true }), 3000);
    const wait = [1000, 2000, 5000, 10000][Math.min(tries++, 3)];
    retry = setTimeout(() => listeners.size && open(), wait);
  };
}
function close() { es?.close(); es = null; clearTimeout(retry); clearTimeout(lostTimer); set({ lost: false }); }

// Subscribe to the stream; returns the unsubscribe. The stream opens with the first listener and closes with the last.
export function onLive(l: Listener): () => void {
  listeners.add(l); open();
  return () => { listeners.delete(l); if (!listeners.size) close(); };
}
export function useLiveStatus() {
  return useSyncExternalStore((f) => (watchers.add(f), () => watchers.delete(f)), () => state);
}
