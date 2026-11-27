// Icon markup from the draft-b sprite (dashboard.html), keyed by name without the i- prefix.
export const ICON_PATHS = {
  "copy": "<rect x=\"9\" y=\"9\" width=\"13\" height=\"13\" rx=\"2\"/><path d=\"M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1\"/>",
  "dash": "<rect x=\"3\" y=\"3\" width=\"7\" height=\"9\" rx=\"1.5\"/><rect x=\"14\" y=\"3\" width=\"7\" height=\"5\" rx=\"1.5\"/><rect x=\"14\" y=\"12\" width=\"7\" height=\"9\" rx=\"1.5\"/><rect x=\"3\" y=\"16\" width=\"7\" height=\"5\" rx=\"1.5\"/>",
  "jobs": "<path d=\"M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01\"/>",
  "flow": "<rect x=\"3\" y=\"3\" width=\"6\" height=\"6\" rx=\"1.5\"/><rect x=\"15\" y=\"15\" width=\"6\" height=\"6\" rx=\"1.5\"/><path d=\"M6 9v6a3 3 0 0 0 3 3h6\"/>",
  "lib": "<path d=\"M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z\"/>",
  "worker": "<rect x=\"3\" y=\"4\" width=\"18\" height=\"6\" rx=\"1.5\"/><rect x=\"3\" y=\"14\" width=\"18\" height=\"6\" rx=\"1.5\"/><path d=\"M7 7h.01M7 17h.01\"/>",
  "settings": "<path d=\"M4 7h10M18 7h2M4 17h2M10 17h10\"/><circle cx=\"16\" cy=\"7\" r=\"2\"/><circle cx=\"8\" cy=\"17\" r=\"2\"/>",
  "search": "<circle cx=\"11\" cy=\"11\" r=\"7\"/><path d=\"M20 20l-4-4\"/>",
  "plus": "<path d=\"M12 5v14M5 12h14\"/>",
  "play": "<path d=\"M7 4l13 8-13 8z\"/>",
  "alert": "<path d=\"M12 3l10 18H2z\"/><path d=\"M12 10v5M12 18h.01\"/>",
  "check": "<path d=\"M5 12l5 5 9-10\"/>",
  "refresh": "<path d=\"M20 11a8 8 0 0 0-14-4L4 9M4 4v5h5M4 13a8 8 0 0 0 14 4l2-2M20 20v-5h-5\"/>",
  "flask": "<path d=\"M9 3h6M10 3v6l-5 9a2 2 0 0 0 2 3h10a2 2 0 0 0 2-3l-5-9V3\"/>",
  "folder": "<path d=\"M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z\"/>",
  "chev": "<path d=\"M9 6l6 6-6 6\"/>",
  "sun": "<circle cx=\"12\" cy=\"12\" r=\"4\"/><path d=\"M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5L19 19M5 19l1.5-1.5M17.5 6.5L19 5\"/>",
  "more": "<path d=\"M5 12h.01M12 12h.01M19 12h.01\" stroke-width=\"3\"/>",
  "x": "<path d=\"M6 6l12 12M18 6L6 18\"/>",
  "film": "<rect x=\"3\" y=\"4\" width=\"18\" height=\"16\" rx=\"2\"/><path d=\"M7 4v16M17 4v16M3 9h4M3 15h4M17 9h4M17 15h4\"/>",
  "menu": "<path d=\"M4 7h16M4 12h16M4 17h16\"/>",
} as const;

export type IconName = keyof typeof ICON_PATHS;
