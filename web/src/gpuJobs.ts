// The built-in worker's "Graphics jobs don't wait for processor jobs" switch (settings.gpuJobsSeparate). Its wording
// lives here, apart from Workers.tsx, so the tests can run under `node --test` without a DOM.

export const GPU_LABEL = "Graphics jobs don't wait for processor jobs";
export const GPU_HINT =
  "A file that converts on the graphics card can start while processor jobs fill Files at once. It uses more memory. Off: every file counts toward Files at once.";

export const gpuSaved = (on: boolean): string => "Saved: graphics jobs " + (on ? "don't wait" : "wait") + " for processor jobs.";
