import { describe, expect, it } from "vitest";
import { ago, bytes, plural, took, until } from "./lib";
import { parseRoute } from "./router";

describe("format", () => {
  const now = Date.parse("2026-10-02T12:00:00Z");
  it("ago", () => {
    expect(ago("2026-10-02T11:59:30Z", now)).toBe("just now");
    expect(ago("2026-10-02T09:00:00Z", now)).toBe("3h ago");
    expect(ago("2026-09-28T12:00:00Z", now)).toBe("4d ago");
  });
  it("bytes", () => {
    expect(bytes(999)).toBe("999 B");
    expect(bytes(1500)).toBe("1.5 kB");
    expect(bytes(3_200_000_000)).toBe("3.2 GB");
  });
  it("until and took", () => {
    expect(until("2026-10-02T15:00:00Z", now)).toBe("in 3h");
    expect(until("2026-10-02T12:00:30Z", now)).toBe("any moment");
    expect(took("2026-10-02T12:00:00Z", "2026-10-02T12:04:12Z")).toBe("4m 12s");
    expect(took("2026-10-02T12:00:00Z", "2026-10-02T14:30:00Z")).toBe("2h 30m");
  });
  it("plural", () => {
    expect(plural(1, "item")).toBe("1 item");
    expect(plural(3, "item")).toBe("3 items");
  });
});

describe("router", () => {
  it("parses routes", () => {
    expect(parseRoute("/")).toEqual({ page: "home" });
    expect(parseRoute("/runs")).toEqual({ page: "runs" });
    expect(parseRoute("/restores")).toEqual({ page: "restores" });
    expect(parseRoute("/restores/ledger-20261009-1200-5")).toEqual({
      page: "restore",
      name: "ledger-20261009-1200-5",
    });
    expect(parseRoute("/runs/12")).toEqual({ page: "run", id: 12 });
    expect(parseRoute("/runs/x")).toEqual({ page: "runs" });
    expect(parseRoute("/sources/my%20app")).toEqual({ page: "source", name: "my app" });
    expect(parseRoute("/settings")).toEqual({ page: "settings" });
    expect(parseRoute("/nope")).toEqual({ page: "home" });
  });
});
