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

export interface Retention {
  latest: number;
  hourly: number;
  daily: number;
  weekly: number;
  monthly: number;
  annual: number;
}

/** A source entry: a folder or database to back up, or changes to a found folder. */
export interface SourceConfig {
  name: string;
  path?: string;
  strategy: string;
  excludes?: string[] | null;
  container?: string;
  volume?: string;
  command?: string;
  skip?: boolean;
}

/** A watched folder: every folder inside it is a source. */
export interface RootConfig {
  path: string;
  skip?: string[] | null;
}

export interface Config {
  every: string;
  stale_after: string;
  roots: RootConfig[];
  sources: SourceConfig[];
  excludes: string[];
  engine: { type: string; container: string };
  staging: string;
  retention: Retention;
}

export interface Coverage {
  source?: string;
  excluded?: boolean;
  root?: boolean;
  contains?: number;
}

export interface Folder extends Coverage {
  name: string;
  path: string;
}

export interface Listing extends Coverage {
  path: string;
  parent?: string;
  roots: string[];
  folders: Folder[];
}

export interface Suggestion {
  container: string;
  image: string;
  running: boolean;
  kind: "folder" | "database" | "volume";
  path?: string;
  volume?: string;
  strategy?: string;
  mount: string;
}
