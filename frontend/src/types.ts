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
  /** Its snapshots in the local repository, and why there are none. */
  local: { id: string; path: string }[];
  local_error?: string;
}

export type RunKind = "backup" | "verify" | "restore";

export interface Run {
  id: number;
  kind: RunKind;
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
  hooks?: Hooks;
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
  kind?: RunKind;
  current?: string;
  next: string;
  next_kind: RunKind;
  last_run?: Run;
  last_verify?: Run;
  /** The local repository's folder ("" for none) and the last run there. */
  local?: string;
  local_info?: { size: number; at: string; ok: number; total: number };
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
  hooks?: Hooks;
  skip?: boolean;
}

/** Commands run with sh -c in a container around a source's snapshot. */
export interface Hooks {
  container?: string;
  before?: string;
  after?: string;
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
  restores: string;
  retention: Retention;
  verify: { every: string; percent: number; heartbeat?: string };
  /** A second repository in a folder on this server; no path turns it off. */
  local: { path?: string; heartbeat?: string; config_file: string; cache: string };
  /** Paths whose snapshots are deleted on the first verify on or after a date. */
  retire: { path: string; after: string }[];
  /** Container paths and volumes not suggested: left out on purpose. */
  ignored: string[];
}

/** Snapshots of a path Keep doesn't back up now. */
export interface OtherSource {
  path: string;
  snapshots: number;
  oldest: string;
  newest: string;
  latest: string;
  size: number;
  files: number;
  retire_after?: string;
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

/** A backup of a source that can be restored. */
export interface RestorePoint {
  run: number;
  taken: string;
  size: number;
  files: number;
  /** In the local repository: a fast restore. */
  local?: boolean;
}

export interface RestoreSpec {
  source?: string;
  run?: number;
  snapshot?: string;
  path?: string;
}

/** A restore folder. */
export interface RestoreInfo {
  name: string;
  dir: string;
  label: string;
  run?: number;
  taken: string;
  path?: string;
  run_id: number;
  created: string;
  expires: string;
  size: number;
  files: number;
  complete: boolean;
}

export interface RestoreEntry {
  name: string;
  path: string;
  dir: boolean;
  size: number;
  modified: string;
}
