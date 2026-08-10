import { describe, expect, it } from "vitest";
import { DEFAULT_SORT, sortObjects, sortPrefixes } from "./sorting";

type Fixture = {
  objectKey: string;
  size: number;
  lastModified: Date;
};

const obj = (
  objectKey: string,
  size: number | null,
  lastModified: string | null
): Fixture =>
  ({
    objectKey,
    size: size as unknown as number,
    // lastModified is a string at runtime; the fixture intentionally mirrors
    // that rather than the (lying) Date type — see sorting.ts.
    lastModified: lastModified as unknown as Date,
  }) as Fixture;

describe("sortObjects", () => {
  it("sorts by name ascending", () => {
    const objects = [obj("c.txt", 1, null), obj("a.txt", 1, null), obj("b.txt", 1, null)];
    const sorted = sortObjects(objects, { column: "name", direction: "asc" });
    expect(sorted.map((o) => o.objectKey)).toEqual(["a.txt", "b.txt", "c.txt"]);
  });

  it("sorts by name descending", () => {
    const objects = [obj("c.txt", 1, null), obj("a.txt", 1, null), obj("b.txt", 1, null)];
    const sorted = sortObjects(objects, { column: "name", direction: "desc" });
    expect(sorted.map((o) => o.objectKey)).toEqual(["c.txt", "b.txt", "a.txt"]);
  });

  it("sorts by size ascending", () => {
    const objects = [obj("a", 300, null), obj("b", 100, null), obj("c", 200, null)];
    const sorted = sortObjects(objects, { column: "size", direction: "asc" });
    expect(sorted.map((o) => o.objectKey)).toEqual(["b", "c", "a"]);
  });

  it("sorts by size descending", () => {
    const objects = [obj("a", 300, null), obj("b", 100, null), obj("c", 200, null)];
    const sorted = sortObjects(objects, { column: "size", direction: "desc" });
    expect(sorted.map((o) => o.objectKey)).toEqual(["a", "c", "b"]);
  });

  it("sorts by lastModified given string inputs (the runtime shape)", () => {
    const objects = [
      obj("a", 1, "2026-01-03T00:00:00Z"),
      obj("b", 1, "2026-01-01T00:00:00Z"),
      obj("c", 1, "2026-01-02T00:00:00Z"),
    ];
    const asc = sortObjects(objects, { column: "lastModified", direction: "asc" });
    expect(asc.map((o) => o.objectKey)).toEqual(["b", "c", "a"]);

    const desc = sortObjects(objects, { column: "lastModified", direction: "desc" });
    expect(desc.map((o) => o.objectKey)).toEqual(["a", "c", "b"]);
  });

  it("tolerates null size without throwing, placing it consistently", () => {
    const objects = [obj("a", 300, null), obj("b", null, null), obj("c", 100, null)];
    expect(() =>
      sortObjects(objects, { column: "size", direction: "asc" })
    ).not.toThrow();

    const asc = sortObjects(objects, { column: "size", direction: "asc" });
    // null normalises to 0, so it sorts first ascending.
    expect(asc.map((o) => o.objectKey)).toEqual(["b", "c", "a"]);
  });

  it("tolerates null lastModified without throwing, placing it consistently", () => {
    const objects = [
      obj("a", 1, "2026-01-02T00:00:00Z"),
      obj("b", 1, null),
      obj("c", 1, "2026-01-01T00:00:00Z"),
    ];
    expect(() =>
      sortObjects(objects, { column: "lastModified", direction: "asc" })
    ).not.toThrow();

    const asc = sortObjects(objects, { column: "lastModified", direction: "asc" });
    // null normalises to 0 (epoch), so it sorts first ascending.
    expect(asc.map((o) => o.objectKey)).toEqual(["b", "c", "a"]);
  });

  it("tolerates an unparsable lastModified string without corrupting the sort", () => {
    // `new Date("not-a-date").getTime()` is NaN, not null — a distinct risk
    // from the null-pointer case above. A NaN reaching the comparator's
    // return value silently corrupts Array#sort, so this must be normalised
    // to the same 0 sentinel as a missing value, not merely avoid throwing.
    const objects = [
      obj("d", 1, "2026-01-01T00:00:00Z"),
      obj("c", 1, "not-a-date"),
      obj("b", 1, null),
      obj("a", 1, "2026-01-02T00:00:00Z"),
    ];
    expect(() =>
      sortObjects(objects, { column: "lastModified", direction: "asc" })
    ).not.toThrow();

    const asc = sortObjects(objects, { column: "lastModified", direction: "asc" });
    // "not-a-date" and null both normalise to 0 (epoch) and then tie-break by
    // objectKey ascending: b before c. Real dates follow in date order.
    expect(asc.map((o) => o.objectKey)).toEqual(["b", "c", "d", "a"]);
  });

  it("does not mutate its input array", () => {
    const objects = [obj("c", 1, null), obj("a", 1, null), obj("b", 1, null)];
    const original = objects.map((o) => o.objectKey);
    sortObjects(objects, { column: "name", direction: "asc" });
    expect(objects.map((o) => o.objectKey)).toEqual(original);
  });

  it("breaks ties on size by objectKey ascending", () => {
    const objects = [obj("z", 100, null), obj("a", 100, null), obj("m", 100, null)];
    const sorted = sortObjects(objects, { column: "size", direction: "asc" });
    expect(sorted.map((o) => o.objectKey)).toEqual(["a", "m", "z"]);

    // Even descending on the sorted column, ties still break by objectKey
    // ascending — the plan calls for a stable, predictable tie order, not a
    // reversed one.
    const desc = sortObjects(objects, { column: "size", direction: "desc" });
    expect(desc.map((o) => o.objectKey)).toEqual(["a", "m", "z"]);
  });

  it("DEFAULT_SORT is name ascending, matching S3 ListObjectsV2 order", () => {
    expect(DEFAULT_SORT).toEqual({ column: "name", direction: "asc" });
  });
});

describe("sortPrefixes", () => {
  it("returns name-ascending order by default", () => {
    const sorted = sortPrefixes(["c/", "a/", "b/"], "asc");
    expect(sorted).toEqual(["a/", "b/", "c/"]);
  });

  it("honours direction when the caller passes desc", () => {
    const sorted = sortPrefixes(["c/", "a/", "b/"], "desc");
    expect(sorted).toEqual(["c/", "b/", "a/"]);
  });

  it("does not mutate its input array", () => {
    const prefixes = ["c/", "a/", "b/"];
    const original = [...prefixes];
    sortPrefixes(prefixes, "desc");
    expect(prefixes).toEqual(original);
  });
});
