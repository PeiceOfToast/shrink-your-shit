// Notifications (UI-API-CONTRACT-v2.md "## Notifications", agreed 11-19). Replaces the placeholder integrations.notifications.
export type NotifyKind = "webhook" | "discord";
export interface NotifyEvents { jobFailed: boolean; needsYou: boolean; fileFinished: boolean; batchFinished: boolean; workerOffline: boolean; encoderFellBack: boolean }
export interface NotifyTarget {
  id: string; name: string; kind: NotifyKind;
  urlShown: string; // the URL after its host is hidden; the full URL is write-only
  enabled: boolean;
  events: NotifyEvents;
  lastResult: { ok: boolean; message: string; at: string } | null;
  failingSince: string | null;
}
export type NotifySummaryMode = "off" | "drained" | "daily" | "weekly";
export interface NotifySummary { mode: NotifySummaryMode; at: string } // at: "HH:MM", used by daily and weekly
export interface NotifySettings { targets: NotifyTarget[]; summary: NotifySummary; linkBase: string | null }
export interface NotifyTargetInput { name: string; kind: NotifyKind; url: string; enabled?: boolean; events?: NotifyEvents }
// {id, url?} tests a saved target (url tests an edited address without saving it); {kind, url} tests a new one.
export type NotifyTestInput = { id: string; url?: string } | { kind: NotifyKind; url: string };
export interface NotifyTestResult { ok: boolean; message: string }
