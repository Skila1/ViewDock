import { request } from "./client";

export type BackupKind = "sqlite-snapshot" | "logical-json";
export type BackupTrigger = "manual" | "scheduled" | "pre-restore" | "cli";

export type BackupSummary = {
  id: string;
  kind: BackupKind;
  trigger: BackupTrigger;
  app_version: string;
  dialect: string;
  schema_version: number;
  created_at: string;
  created_by?: string;
  master_key_id?: string;
  size: number;
  files: number;
  tables: number;
  rows: number;
};

export type BackupStatus = {
  destination: "local" | "s3";
  location: string;
  schedule_hours: number;
  retention: number;
  running: boolean;
  next_run?: string;
  last_backup_at?: string;
  last_failure_at?: string;
  dialect: string;
  master_key_id?: string;
  notice: string;
};

export type BackupList = {
  status: BackupStatus;
  items: BackupSummary[];
  destination_error?: string;
};

export type BackupValidation = {
  id: string;
  valid: boolean;
  problems: string[];
  kind: BackupKind;
  schema_version: number;
  current_schema: number;
  schema_compatible: boolean;
  restore_mode: "file" | "rows";
  current_database_empty: boolean;
  master_key_matches?: boolean;
  files_checked: number;
  bytes: number;
};

export type DestinationCheck = { ok: boolean; message: string };

const base = "/api/v1/admin/backups";
const item = (id: string) => `${base}/${encodeURIComponent(id)}`;

export const backupsApi = {
  list: async (): Promise<BackupList> => {
    const out = await request<BackupList>(base);
    return { ...out, items: Array.isArray(out?.items) ? out.items : [] };
  },
  create: () => request<BackupSummary>(base, { method: "POST" }),
  validate: (id: string) => request<BackupValidation>(`${item(id)}/validate`, { method: "POST" }),
  remove: (id: string) => request<null>(item(id), { method: "DELETE" }),
  checkDestination: () => request<DestinationCheck>(`${base}/destination/check`, { method: "POST" }),
  downloadUrl: (id: string) => `${item(id)}/download`,
};
