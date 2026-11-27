// The File type hint, shared by the guided page and the editor: what each file type keeps, then the reason each greyed
// type is ruled out (one sentence per reason, no prefix naming the type).
export const FILE_TYPE_HINT = "MKV holds every track and subtitle. MP4 plays on more devices but drops some subtitles.";

export function fileTypeHint(reasons: string[]): string {
  return [FILE_TYPE_HINT, ...reasons].join(" ");
}
