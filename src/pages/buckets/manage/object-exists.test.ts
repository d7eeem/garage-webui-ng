import { describe, expect, it } from "vitest";
import { classifyObjectProbe } from "./hooks";
import { APIError } from "@/lib/api";

describe("classifyObjectProbe", () => {
  it("returns 'present' on success", () => {
    expect(classifyObjectProbe(true, null)).toBe("present");
  });

  it("returns 'missing' when the error is a 404 APIError", () => {
    const err = new APIError("not found", 404);
    expect(classifyObjectProbe(false, err)).toBe("missing");
  });

  it(
    "returns 'unknown' on a 500 — the no-read+write-key case; a regression " +
      "here makes the UI accuse users of missing files that are present",
    () => {
      const err = new APIError("internal error", 500);
      expect(classifyObjectProbe(false, err)).toBe("unknown");
    }
  );

  it("returns 'unknown' on a 403 APIError", () => {
    const err = new APIError("forbidden", 403);
    expect(classifyObjectProbe(false, err)).toBe("unknown");
  });

  it("returns 'unknown' for a plain Error with no status", () => {
    const err = new Error("boom");
    expect(classifyObjectProbe(false, err)).toBe("unknown");
  });

  it("returns 'unknown' when isSuccess is false and error is null", () => {
    expect(classifyObjectProbe(false, null)).toBe("unknown");
  });
});
