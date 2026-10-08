export interface Session {
  authenticated: boolean;
  /** Foyer, the homelab's start page (HOMEPAGE_URL). */
  foyer_url?: string;
}

export type RunStatus = "running" | "ok" | "warn" | "failed";
export type SourceState = "ok" | "running" | "stale" | "errors" | "never";

export interface RunSource {
  run_id: number;
  name: string;
  strategy: string;
  status: RunStatus;
  started: string;
  finished: string;
  size: number;
  files: number;
  databases: number;
  message: string;
  snapshots: string[];
}

export interface Run {
  id: number;
  started: string;
  finished: string;
  trigger: string;
  status: RunStatus;
  summary: string;
  size: number;
  files: number;
  sources?: RunSource[];
}

export interface SourceStatus {
  name: string;
  path?: string;
  host_path?: string;
  strategy: string;
  excludes?: string[];
  container?: string;
  volume?: string;
  discovered: boolean;
  partial: boolean;
  state: SourceState;
  latest?: RunSource;
  success?: RunSource;
}

export interface Overview {
  engine: string;
  every: string;
  stale_after: string;
  sources: SourceStatus[];
  running?: number;
  current?: string;
  next: string;
  last_run?: Run;
  repo?: { size: number; at: string };
  config_error?: string;
  config_file: string;
}

export interface LogLine {
  id: number;
  at: string;
  level: "info" | "warn" | "error";
  text: string;
}
