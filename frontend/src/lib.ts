export type Tone = "good" | "warn" | "bad" | "accent" | "";

export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const s = Math.max(0, (now - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 48 * 3600) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** Bytes in decimal units, like the server's: 1.2 GB. */
export function bytes(n: number): string {
  if (n < 1000) return `${n} B`;
  const units = ["kB", "MB", "GB", "TB", "PB"];
  let v = n / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}

/** How long until a time: "in 3h". */
export function until(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const s = (new Date(iso).getTime() - now) / 1000;
  if (s < 60) return "any moment";
  if (s < 3600) return `in ${Math.floor(s / 60)}m`;
  if (s < 48 * 3600) return `in ${Math.floor(s / 3600)}h`;
  return `in ${Math.floor(s / 86400)}d`;
}

/** A duration between two times: 4m 12s. */
export function took(from: string, to: string): string {
  const s = Math.max(0, Math.round((new Date(to).getTime() - new Date(from).getTime()) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

export function runTone(status: string): Tone {
  switch (status) {
    case "ok":
      return "good";
    case "warn":
      return "warn";
    case "failed":
      return "bad";
    case "running":
      return "accent";
  }
  return "";
}

export function stateTone(state: string): Tone {
  switch (state) {
    case "ok":
      return "good";
    case "running":
      return "accent";
    case "never":
    case "stale":
      return "warn";
    case "errors":
      return "bad";
  }
  return "";
}

export const STATE_LABEL: Record<string, string> = {
  ok: "Backed up",
  running: "Running",
  stale: "Stale",
  errors: "Problem",
  never: "Never",
};

export const RUN_LABEL: Record<string, string> = {
  ok: "OK",
  warn: "Warnings",
  failed: "Failed",
  running: "Running",
};
