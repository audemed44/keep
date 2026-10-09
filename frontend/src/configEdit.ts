import type { Config, SourceConfig } from "./types";

/**
 * Edits to the settings, made in the browser and saved whole. Sources are
 * either entries of their own (a folder or a database), or changes to a
 * folder found in a watched folder (same name, no path).
 */

const clone = (c: Config): Config => JSON.parse(JSON.stringify(c));

/** The folder's last path segment, made into a valid source name. */
export function nameFor(path: string): string {
  const base = path.replace(/\/+$/, "").split("/").pop() ?? "";
  return base.replace(/[^a-zA-Z0-9._-]+/g, "-").replace(/^[^a-zA-Z0-9]+/, "") || "source";
}

/** A name not used yet: ledger, ledger-2, … */
export function freeName(c: Config, names: string[], want: string): string {
  const used = new Set([...names, ...c.sources.map((s) => s.name)]);
  if (!used.has(want)) return want;
  for (let i = 2; ; i++) if (!used.has(`${want}-${i}`)) return `${want}-${i}`;
}

export function addSource(c: Config, s: SourceConfig): Config {
  const out = clone(c);
  out.sources.push(s);
  return out;
}

export function watchFolder(c: Config, path: string): Config {
  const out = clone(c);
  if (!out.roots.some((r) => r.path === path)) out.roots.push({ path, skip: [] });
  return out;
}

export function unwatchFolder(c: Config, path: string): Config {
  const out = clone(c);
  out.roots = out.roots.filter((r) => r.path !== path);
  return out;
}

/** Sets a source's entry: replaces its own entry, or adds one for a found folder. */
export function setSource(c: Config, s: SourceConfig): Config {
  const out = clone(c);
  const i = out.sources.findIndex((x) => x.name === s.name);
  if (i >= 0) out.sources[i] = s;
  else out.sources.push(s);
  return out;
}

/**
 * Stops backing a source up: an entry of its own goes; a found folder gets
 * an entry that skips it.
 */
export function removeSource(c: Config, name: string, discovered: boolean): Config {
  const out = clone(c);
  out.sources = out.sources.filter((x) => x.name !== name);
  if (discovered) out.sources.push({ name, strategy: "", skip: true });
  return out;
}

/** Brings back a skipped found folder. */
export function restoreSource(c: Config, name: string): Config {
  const out = clone(c);
  out.sources = out.sources.filter((x) => !(x.name === name && x.skip));
  return out;
}

/** Lines of a textarea as a list, without blanks. */
export function lines(text: string): string[] {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
}
