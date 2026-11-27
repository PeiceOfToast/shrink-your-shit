// Shared by the Job log (page 03) and the System log (page 12): the level words, copying and saving text.

export type LogLevelName = "info" | "warn" | "error" | "detail";

export const LEVEL_LABEL: Record<LogLevelName, string> = { info: "INFO", warn: "WARN", error: "ERROR", detail: "DETAIL" };

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

// Saves text as a .txt file; both logs' downloads use it.
export function saveText(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}
