import { describe, expect, it } from "vitest";
import { ago, plural } from "./lib";
import { parseRoute } from "./router";

describe("format", () => {
  const now = Date.parse("2026-10-02T12:00:00Z");
  it("ago", () => {
    expect(ago("2026-10-02T11:59:30Z", now)).toBe("just now");
    expect(ago("2026-10-02T09:00:00Z", now)).toBe("3h ago");
    expect(ago("2026-09-28T12:00:00Z", now)).toBe("4d ago");
  });
  it("plural", () => {
    expect(plural(1, "item")).toBe("1 item");
    expect(plural(3, "item")).toBe("3 items");
  });
});

describe("router", () => {
  it("parses routes", () => {
    expect(parseRoute("/")).toEqual({ page: "home" });
    expect(parseRoute("/about")).toEqual({ page: "about" });
    expect(parseRoute("/nope")).toEqual({ page: "home" });
  });
});
