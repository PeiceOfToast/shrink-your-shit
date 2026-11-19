// The health checks page (UI-API-CONTRACT-v2.md "Doctor", workshop 11-19 07:48Z). bad shows as "Fail".
export interface DoctorCheck { id: string; label: string; status: "ok" | "warn" | "bad"; detail: string; hint: string | null }
export interface Doctor {
  version: string; ranAt: string; encodersTestedAt: string | null;
  encoder: { name: string; hardware: boolean; label: string };
  checks: DoctorCheck[];
}
