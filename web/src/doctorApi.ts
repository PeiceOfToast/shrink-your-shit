// GET /api/v2/doctor (contract "Doctor"): fast checks on every call; the encoder test encodes only with ?retest=1.
// Not on dev yet, so ?mock= answers: ?mock=1 has one Fail and two warnings, ?mock=fresh no library yet.
import { call, MOCK, type Answer } from "./api";
import type { Doctor, DoctorCheck } from "./types/doctor";

const fresh = new URLSearchParams(location.search).get("mock") === "fresh";
let tested = new Date(Date.now() - 2 * 3600000).toISOString();
const c = (id: string, label: string, status: DoctorCheck["status"], detail: string, hint: string | null = null): DoctorCheck => ({ id, label, status, detail, hint });
function mockDoctor(): Doctor {
  return { version: "0.3.1", ranAt: new Date().toISOString(), encodersTestedAt: tested,
    encoder: { name: "libx265", hardware: false, label: "CPU (x265)" },
    checks: [
      c("ffmpeg", "ffmpeg", "ok", "ffmpeg 7.1 found."), c("ffprobe", "ffprobe", "ok", "ffprobe 7.1 found."), c("x265", "x265 encoder", "ok", "ffmpeg has the x265 encoder."),
      c("ssim", "Quality check", "ok", "ffmpeg can measure quality (SSIM)."),
      c("encoder:qsv", "Intel Quick Sync", "bad", "Quick Sync is present but the test encode failed: Device creation failed.", "Give the container /dev/dri (see README, Hardware)."),
      c("encoder:vaapi", "VAAPI", "warn", "No VAAPI device found."),
      c("encoder:nvenc", "NVIDIA NVENC", "absent", "No NVIDIA card found."),
      c("encoder:in-use", "Encoder in use", "ok", "Conversions use the CPU (x265)."),
      c("data", "Data folder", "ok", "Data folder is writable."),
      ...(fresh ? [c("libraries", "Libraries", "warn", "No libraries yet, so there is nothing to convert.", "Add a library in Libraries.")] : [
        c("library:1:access", "Movies", "ok", "Movies: readable and writable."),
        c("library:1:space", "Movies", "warn", "Movies: 3.1 GB free; the largest file waiting needs 9.4 GB.", "Free space or point Temporary files at another disk."),
        c("library:1:hardlinks", "Movies", "ok", "Movies: no hard-linked files."),
        c("library:2:access", "TV", "ok", "TV: readable and writable."),
        c("library:2:space", "TV", "ok", "TV: 412 GB free."),
        c("library:2:hardlinks", "TV", "warn", "TV: 12 files are hard-linked (torrent seeds?). Replacing them won't free space until the other copy goes.", "Leave them, or exclude the seeding folder.")]),
    ] };
}

export async function getDoctor(retest = false): Promise<Answer<Doctor>> {
  if (MOCK) {
    if (retest) tested = new Date().toISOString();
    return new Promise((r) => setTimeout(() => r(mockDoctor()), retest ? 2500 : 300));
  }
  return call("GET", "/api/v2/doctor" + (retest ? "?retest=1" : ""));
}
