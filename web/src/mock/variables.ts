// The variable list for the workflow editor's {{name}} fields (?mock=1). Names and groups are the contract's flat list
// (slices/design-joblog-vars.md section 2); the values are for the sample file Movie (2019).mkv. A run-time name with no
// value here shows as empty in the example, as the page does for a file that has none.
export interface TemplateVar { name: string; description: string; group: string; value: string | null; fields: string[] | null }
export interface VarList { sample: { path: string; builtIn: boolean }; variables: TemplateVar[] }

const v = (name: string, group: string, description: string, value: string | null): TemplateVar => ({ name, description, group, value, fields: null });

export function mockVariables(): VarList {
  return {
    sample: { path: "/media/Movie (2019).mkv", builtIn: false },
    variables: [
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
      v("workflow", "Run", "The workflow's name", "Shrink videos"),
      v("workflow_revision", "Run", "The workflow's revision number", "3"),
      v("worker", "Run", "The worker that ran the job", null),
      v("encoder", "Run", "The encoder used", null),
      v("target_codec", "Run", "The codec the new file uses", "hevc"),
      v("output_path", "Run", "Where the new file was saved", null),
      v("new_size_bytes", "Run", "The new file's size in bytes", null),
      v("saved_percent", "Run", "How much smaller the new file is", null),
    ],
  };
}
