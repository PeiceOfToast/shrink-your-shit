// The variable list for the workflow editor's {{name}} fields (?mock=1). Names and groups are the contract's flat list
// (slices/design-joblog-vars.md section 2); the values are for the sample file Movie (2019).mkv. A run-time name with no
// value here shows as empty in the example, as the page does for a file that has none.
import type { Variable } from "../types/v2";

// secret: a workflow's own secret. It is listed with fields [] (allowed in no field), so the page can say why it is refused.
export interface TemplateVar { name: string; description: string; group: string; value: string | null; fields: string[] | null; secret?: boolean }
export interface VarList { sample: { path: string; builtIn: boolean }; variables: TemplateVar[] }

const v = (name: string, group: string, description: string, value: string | null): TemplateVar => ({ name, description, group, value, fields: null });
// The names a field may use: its fields list, or every field when the list is null (the server's contract, #511).
export const allowedIn = (x: TemplateVar, field: string) => x.fields == null || x.fields.includes(field);
// The closest name, when one is within two edits of the typed one (for "Did you mean" and the Use button).
export function closest(name: string, names: string[]): string | undefined {
  const dist = (a: string, b: string) => {
    let prev = Array.from({ length: b.length + 1 }, (_, j) => j);
    for (let i = 1; i <= a.length; i++) {
      const cur = [i];
      for (let j = 1; j <= b.length; j++) cur[j] = Math.min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1));
      prev = cur;
    }
    return prev[b.length];
  };
  let best: string | undefined, bestD = 3;
  for (const n of names) { const d = dist(name, n); if (d < bestD) { bestD = d; best = n; } }
  return best;
}

// own: the workflow's own variables (its draft's list). The server lists them last, group "Workflow"; a secret's value is never sent.
export function mockVariables(own: Variable[] = []): VarList {
  return {
    sample: { path: "/media/Movie (2019).mkv", builtIn: true },
    variables: [
      ...builtIns(),
      ...own.map((x) => v(x.name, "Workflow", x.description ?? "", x.type === "secret" || x.value == null ? null : String(x.value))),
    ],
  };
}

// The engine's built-in names, as the server lists them (the field-only ones are left off the list; checkVariables still refuses them).
function builtIns(): TemplateVar[] {
  return [
      v("title", "File", "The file name without its extension", "Movie (2019)"),
      v("file_name", "File", "The file name with its extension", "Movie (2019).mkv"),
      v("ext", "File", "The file's extension", "mkv"),
      v("folder", "File", "The folder the file is in", "/media"),
      v("path", "File", "The full path of the file", "/media/Movie (2019).mkv"),
      v("size_bytes", "File", "The file's size in bytes", "4215883110"),
      v("library", "Library", "The name of the library", "Movies"),
      v("library_root", "Library", "The library's folder", "/media"),
      v("video_codec", "Video", "The video's codec", "h264"),
      v("width", "Video", "The width in pixels", "1920"),
      v("height", "Video", "The height in pixels", "1080"),
      v("resolution", "Video", "The resolution, such as 1080p", "1080p"),
      v("hdr", "Video", "Whether the video is HDR", "no"),
      v("duration", "Video", "The running time", "1:45:12"),
      v("bitrate_kbps", "Video", "The video's bitrate in kbps", "8400"),
      v("container", "Video", "The file type the file is in now", "mkv"),
      v("audio_languages", "Audio", "The audio languages", "English, German"),
      v("audio_codecs", "Audio", "The audio codecs", "aac, ac3"),
      v("audio_channels", "Audio", "The channel counts", "2, 6"),
      v("subtitle_languages", "Subtitles", "The subtitle languages", "English, English SDH, German"),
      { ...v("sub_lang", "Subtitles", "The language of the subtitle being saved", "English"), fields: ["srtName"] }, // W4b: only in the subtitle file's name
      { ...v("sub_kind", "Subtitles", "The kind of the subtitle being saved, such as SDH", "SDH"), fields: ["srtName"] },
      v("workflow", "Run", "The workflow's name", "Shrink videos"),
      v("workflow_revision", "Run", "The workflow's revision number", "3"),
      v("worker", "Run", "The worker that ran the job", null),
      v("encoder", "Run", "The encoder used", null),
      v("target_codec", "Run", "The codec the new file uses", "hevc"),
      v("output_path", "Run", "Where the new file was saved", null),
      v("new_size_bytes", "Run", "The new file's size in bytes", null),
      v("saved_percent", "Run", "How much smaller the new file is", null),
  ];
}
