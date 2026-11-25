// A job whose library was removed keeps its row. The list then sends the saved folder text as the library name and the
// old id, which no loaded library has. "Removed" needs the library list, so nothing reads as removed before it loads.
import type { ID } from "./types/v2.ts";

export const libraryGone = (libraryId: ID | undefined, libs: { id: ID }[] | undefined): boolean =>
  !!libraryId && libraryId !== "0" && !!libs && !libs.some((l) => l.id === libraryId);

// The drawer's detail has no library object once the library is gone (the server adds it only when the lookup works).
export const detailLibraryGone = (detail: { library?: unknown } | null | undefined, libraryId: ID | undefined): boolean =>
  !!detail && !detail.library && !!libraryId && libraryId !== "0";
