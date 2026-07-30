import { describe, expect, it } from "vitest";
import { compareByFirstAlias, matchesBucketSearch } from "./page";

describe("compareByFirstAlias", () => {
  it("sorts by first alias", () => {
    const items = [{ aliases: ["b"] }, { aliases: ["a"] }];
    const sorted = [...items].sort(compareByFirstAlias);
    expect(sorted).toEqual([{ aliases: ["a"] }, { aliases: ["b"] }]);
  });

  it("does not throw for an alias-less bucket and sorts it first", () => {
    const items = [{ aliases: [] }, { aliases: ["a"] }];
    let sorted: typeof items = [];
    expect(() => {
      sorted = [...items].sort(compareByFirstAlias);
    }).not.toThrow();
    expect(sorted).toEqual([{ aliases: [] }, { aliases: ["a"] }]);
  });

  it("does not throw when both entries have empty aliases", () => {
    const items = [{ aliases: [] }, { aliases: [] }];
    expect(() => [...items].sort(compareByFirstAlias)).not.toThrow();
  });
});

describe("matchesBucketSearch", () => {
  const bucket = { id: "01deadbeef", aliases: ["backups"] };

  it('matches an uppercase query "BACK" against a lowercase alias', () => {
    expect(matchesBucketSearch(bucket, "BACK")).toBe(true);
  });

  it('matches a lowercase query "back" against a mixed-case alias', () => {
    const mixedCaseBucket = { id: "01deadbeef", aliases: ["Backups"] };
    expect(matchesBucketSearch(mixedCaseBucket, "back")).toBe(true);
  });

  it("does not match an unrelated query", () => {
    expect(matchesBucketSearch(bucket, "zzz")).toBe(false);
  });
});
