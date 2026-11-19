// Backup and restore (UI-API-CONTRACT-v2.md "Backup", workshop 11-19 07:48Z).
export type BackupReason = "manual" | "before_update" | "before_migration" | "before_import";
export interface Backup { id: string; name: string; createdAt: string; reason: BackupReason; appVersion: string; sizeBytes: number }
export interface BackupContents { settings: boolean; workflows: number; libraries: number; shares: number; connections: number; jobs: number }
export interface BackupPreview { previewId: string; formatVersion: number; appVersion: string; createdAt: string; contents: BackupContents; warnings: string[] }
export interface BackupList { backups: Backup[]; keep: number }
export interface BackupApplied { restored: BackupContents; safetyBackup: Backup }
