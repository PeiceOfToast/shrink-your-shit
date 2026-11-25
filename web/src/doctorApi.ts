// GET /api/v2/doctor (contract "Doctor"): fast checks on every call; the encoder test encodes only with ?retest=1.
// ?mock= answers with the server's own words (internal/doctor/api.go, dev #660): ?mock=1 has one Fail and two warnings, ?mock=fresh no library yet.
import { call, MOCK, type Answer } from "./api";
import type { Doctor, DoctorCheck } from "./types/doctor";

const fresh = new URLSearchParams(location.search).get("mock") === "fresh";
const calm = new URLSearchParams(location.search).has("calm"); // D2: ?calm is an all-good install (no Fail, no warning)
let tested = new Date(Date.now() - 2 * 3600000).toISOString();
const c = (id: string, label: string, status: DoctorCheck["status"], detail: string, hint: string | null = null): DoctorCheck => ({ id, label, status, detail, hint });
function mockDoctor(): Doctor {
  return { version: "0.3.1", ranAt: new Date().toISOString(), encodersTestedAt: tested,
    encoder: { name: "libx265", hardware: false, label: "CPU (x265)" },
    checks: [
      c("ffmpeg", "ffmpeg", "ok", "ffmpeg 7.1 found."), c("ffprobe", "ffprobe", "ok", "ffprobe 7.1 found."),
      c("x265", "HEVC encoder (libx265)", "ok", "This ffmpeg has the HEVC encoder (x265)."),
      c("ssim", "Picture check (ssim)", "ok", "This ffmpeg has the picture-check filter (ssim)."),
      c("encoder:qsv", "Graphics encoder: Intel Quick Sync", "bad", "Intel Quick Sync is present but the test encode failed: Device creation failed.", "Give the container /dev/dri (see README, Hardware)."),
      c("encoder:vaapi", "Graphics encoder: VA-API", "warn", "VA-API is present, but ffmpeg has no hevc_vaapi encoder."),
      c("encoder:nvenc", "Graphics encoder: NVIDIA NVENC", "absent", "No NVIDIA card found. That is fine."),
      c("encoder:in-use", "Encoder in use", "ok", "Conversions use the CPU (x265)."),
      c("data", "Data folder", "ok", "Data folder is writable."),
      ...(fresh ? [c("libraries", "Libraries", "warn", "No libraries yet. Add one on the Libraries page.")] : [
        c("library:1:access", "Library access", "ok", "Movies: readable and writable."),
        c("library:1:space", "Library free space", "warn", "Movies: 3.1 GB free; the largest video here needs 9.4 GB.", "Free space or point Temporary files at another disk."),
        c("library:1:hardlinks", "Hard-linked files", "ok", "Movies: no hard-linked files."),
        c("library:2:access", "Library access", "ok", "TV: readable and writable."),
        c("library:2:space", "Library free space", "ok", "TV: enough free space."),
        c("library:2:hardlinks", "Hard-linked files", "warn", "TV: 12 files are shared with another folder (for example by Radarr). We will leave them alone, because changing one would break the link.", "Leave them, or exclude the seeding folder.")]),
    ].filter((k) => !calm || k.status === "ok" || k.status === "absent") };
}

export async function getDoctor(retest = false): Promise<Answer<Doctor>> {
  if (MOCK) {
    if (retest) tested = new Date().toISOString();
    return new Promise((r) => setTimeout(() => r(mockDoctor()), retest ? 2500 : 300));
  }
  return call("GET", "/api/v2/doctor" + (retest ? "?retest=1" : ""));
}
