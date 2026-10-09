import type {
  Config,
  Listing,
  LogLine,
  OtherSource,
  Overview,
  RestoreEntry,
  RestoreInfo,
  RestorePoint,
  RestoreSpec,
  Run,
  RunSource,
  Session,
  Suggestion,
} from "./types";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

/** Called when the session has expired, so the app can show the sign-in. */
let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: "same-origin", ...init });
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      message = (await res.json()).error ?? message;
    } catch {
      // not JSON
    }
    if (res.status === 401 && !path.startsWith("/api/session")) onUnauthorized();
    throw new ApiError(message, res.status);
  }
  if (res.status === 204) return undefined as T;
  const type = res.headers.get("Content-Type") ?? "";
  return (type.includes("json") ? res.json() : res.text()) as Promise<T>;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const api = {
  session: () => request<Session>("/api/session"),
  login: (token: string) => request<Session>("/api/session", json("POST", { token })),
  logout: () => request<void>("/api/session", { method: "DELETE" }),

  overview: () => request<Overview>("/api/overview"),
  config: () => request<{ config: Config; roots: string[] }>("/api/config"),
  saveConfig: (c: Config) =>
    request<{ config: Config; roots: string[] }>("/api/config", json("PUT", c)),
  browse: (path = "") => request<Listing>(`/api/browse?path=${encodeURIComponent(path)}`),
  suggestions: () => request<Suggestion[]>("/api/suggestions"),
  runs: (before = 0) => request<Run[]>(`/api/runs${before ? `?before=${before}` : ""}`),
  run: (id: number) => request<Run>(`/api/runs/${id}`),
  runLog: (id: number, after = 0) => request<LogLine[]>(`/api/runs/${id}/log?after=${after}`),
  runNow: (kind: "backup" | "verify" = "backup") =>
    request<{ id: number }>("/api/runs", json("POST", { kind })),
  sourceHistory: (name: string) =>
    request<RunSource[]>(`/api/sources/${encodeURIComponent(name)}/history`),

  restorePoints: (name: string) =>
    request<{ points: RestorePoint[]; listed: string }>(
      `/api/sources/${encodeURIComponent(name)}/points`,
    ),
  repository: () => request<{ listed: string; others: OtherSource[] }>("/api/repository"),
  restores: () => request<{ restores: RestoreInfo[]; keep_days: number }>("/api/restores"),
  startRestore: (spec: RestoreSpec) => request<{ id: number }>("/api/restores", json("POST", spec)),
  deleteRestore: (name: string) =>
    request<void>(`/api/restores/${encodeURIComponent(name)}`, { method: "DELETE" }),
  browseRestore: (name: string, path = "") =>
    request<{ path: string; entries: RestoreEntry[] }>(
      `/api/restores/${encodeURIComponent(name)}/browse?path=${encodeURIComponent(path)}`,
    ),
  /** A download link for a file in a restore (the session cookie signs it). */
  restoreFileURL: (name: string, path: string) =>
    `/api/restores/${encodeURIComponent(name)}/file?path=${encodeURIComponent(path)}`,
};
