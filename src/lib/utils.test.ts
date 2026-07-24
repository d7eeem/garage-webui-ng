import { describe, expect, it } from "vitest";
import { readableBytes, ucfirst } from "@/lib/utils";

describe("ucfirst", () => {
  it("capitalizes the first character", () => {
    expect(ucfirst("hello")).toBe("Hello");
  });

  it("returns null for an empty string", () => {
    expect(ucfirst("")).toBeNull();
  });

  it("returns null for undefined", () => {
    expect(ucfirst(undefined)).toBeNull();
  });

  it("returns null for null", () => {
    expect(ucfirst(null)).toBeNull();
  });
});

describe("readableBytes", () => {
  it('returns "n/a" for null', () => {
    expect(readableBytes(null)).toBe("n/a");
  });

  it('returns "n/a" for undefined', () => {
    expect(readableBytes(undefined)).toBe("n/a");
  });

  it('returns "n/a" for NaN', () => {
    expect(readableBytes(NaN)).toBe("n/a");
  });

  it("formats 0 bytes", () => {
    expect(readableBytes(0)).toBe("0.0 B");
  });

  it("formats an exact kilobyte", () => {
    expect(readableBytes(1024)).toBe("1.0 KB");
  });

  it("formats a fractional kilobyte", () => {
    expect(readableBytes(1536)).toBe("1.5 KB");
  });

  it("supports a divider of 1000 (node capacity display)", () => {
    // Used for node capacities in src/pages/cluster/components/nodes-list.tsx:237
    expect(readableBytes(1000, 1000)).toBe("1.0 KB");
  });

  // Known gap: `sizes` stops at "TB", so anything >= 1 PB indexes out of
  // bounds and renders "undefined". Not fixed here — this plan adds tests
  // only. Un-skip when the sizes array is extended.
  it.skip("formats petabyte-scale values", () => {
    expect(readableBytes(1024 ** 5)).toBe("1.0 PB");
  });
});
