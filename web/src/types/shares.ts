// D: network shares types matching D-API-SHAPE.md (workshop, 11-13 02:20Z).
import type { ID, Time } from "./v2";

export type ShareType = "smb" | "nfs";
export type ShareStatus = "connected" | "not_connected" | "connecting" | "waiting_setup";
export type ShareMode = "app" | "manual" | "docker";
export type Platform = "linux" | "windows" | "darwin";

export interface Share {
  id: ID;
  name: string;
  type: ShareType;
  address: string;
  guest: boolean;
  username: string | null;
  hasPassword: boolean;
  status: ShareStatus;
  since: Time | null;
  reason: { code: string; message: string } | null;
  path: string;
  libraries: { id: ID; name: string; use: "folder" | "output" }[];
  setup: null | {
    kind: "command" | "compose";
    text: string;
    removeText: string;
  };
}

export interface SharesResponse {
  mode: ShareMode;
  platform: Platform;
  nfsSupported: boolean;
  testedPlatform: boolean;
  serverAddress: string | null;
  items: Share[];
}

export interface ShareTestResult {
  ok: boolean;
  address?: string;
  code?: string;
  message?: string;
  folders?: string[];
  moreFolders?: number;
  portOnly?: boolean;
  shares?: string[];
}

export interface ShareTestInput {
  type: ShareType;
  address: string;
  guest: boolean;
  username?: string;
  password?: string;
  id?: ID;
}

export interface ShareCreateInput {
  type: ShareType;
  address: string;
  name: string;
  guest: boolean;
  username?: string;
  password?: string;
}