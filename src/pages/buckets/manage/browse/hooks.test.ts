import { describe, expect, it } from "vitest";
import { getNextObjectPageParam } from "./hooks";
import { GetObjectsResult } from "./types";

const makePage = (nextToken: string | null): GetObjectsResult => ({
  prefixes: [],
  objects: [],
  prefix: "",
  nextToken,
});

describe("getNextObjectPageParam", () => {
  it("returns the token when there is one", () => {
    expect(getNextObjectPageParam(makePage("abc"))).toBe("abc");
  });

  it("returns undefined when there is no next page (nextToken is null)", () => {
    expect(getNextObjectPageParam(makePage(null))).toBe(undefined);
  });

  it("returns the empty string as-is when nextToken is an empty string", () => {
    // "" ?? undefined" evaluates to "" — nullish coalescing only treats
    // null/undefined as nullish, not "". The Go backend's NextToken is a
    // *string that serializes to `null` when unset, never `""`, so this
    // case should not occur in practice. This test pins the behavior the
    // hook actually ships (`lastPage.nextToken ?? undefined`) rather than
    // an idealized one — if the implementation ever changes to
    // `lastPage.nextToken || undefined`, update this expectation to
    // `undefined` to match.
    expect(getNextObjectPageParam(makePage(""))).toBe("");
  });
});
