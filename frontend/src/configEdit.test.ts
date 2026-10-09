import { describe, expect, it } from "vitest";
import {
  addSource,
  freeName,
  nameFor,
  removeSource,
  restoreSource,
  setSource,
  watchFolder,
} from "./configEdit";
import type { Config } from "./types";

const base: Config = {
  every: "12h",
  stale_after: "25h",
  roots: [{ path: "/stack", skip: ["scripts"] }],
  sources: [{ name: "romm", strategy: "", excludes: ["/library"] }],
  excludes: [],
  engine: { type: "kopia", container: "kopia" },
  staging: "/stack/keep/staging",
  retention: { latest: 10, hourly: 48, daily: 7, weekly: 4, monthly: 24, annual: 3 },
  verify: { every: "168h", percent: 5 },
};

describe("config edits", () => {
  it("names folders", () => {
    expect(nameFor("/mnt/hdd/My Photos/")).toBe("My-Photos");
    expect(nameFor("/x/.config")).toBe("config");
    expect(freeName(base, ["ledger"], "ledger")).toBe("ledger-2");
    expect(freeName(base, [], "romm")).toBe("romm-2");
  });
  it("adds, watches and edits without touching the original", () => {
    const c = watchFolder(
      addSource(base, { name: "docs", path: "/mnt/docs", strategy: "files" }),
      "/home",
    );
    expect(c.sources.map((s) => s.name)).toEqual(["romm", "docs"]);
    expect(c.roots.map((r) => r.path)).toEqual(["/stack", "/home"]);
    expect(watchFolder(c, "/home").roots).toHaveLength(2);
    expect(base.sources).toHaveLength(1);
    const e = setSource(c, { name: "romm", strategy: "files", excludes: [] });
    expect(e.sources[0]).toEqual({ name: "romm", strategy: "files", excludes: [] });
  });
  it("removes: own entries go, found folders are skipped", () => {
    const own = removeSource(
      addSource(base, { name: "docs", path: "/d", strategy: "files" }),
      "docs",
      false,
    );
    expect(own.sources.map((s) => s.name)).toEqual(["romm"]);
    const found = removeSource(base, "romm", true);
    expect(found.sources).toEqual([{ name: "romm", strategy: "", skip: true }]);
    expect(restoreSource(found, "romm").sources).toEqual([]);
  });
});
