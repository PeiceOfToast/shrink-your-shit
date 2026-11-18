These ffprobe-style JSON files were WRITTEN BY HAND to cover cases that cannot be produced with
ffmpeg's lavfi test sources: real Dolby Vision and HDR10+ metadata, Blu-ray style bitmap subtitles,
and odd or missing fields. The side-data names (`DOVI configuration record`, `HDR Dynamic Metadata
SMPTE2094-40 (HDR10+)`, `Mastering display metadata`) follow what ffprobe prints. They are not captured
from real files, so they show that the parser reads those shapes, not that every real DV/HDR10+ file looks
like this. The files in `../real/` are real ffprobe output for files generated with ffmpeg.
