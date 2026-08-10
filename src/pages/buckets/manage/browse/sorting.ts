export type SortColumn = "name" | "size" | "lastModified";
export type SortDirection = "asc" | "desc";
export type SortState = { column: SortColumn; direction: SortDirection };

/** The server's own order: S3 ListObjectsV2 returns keys lexicographically. */
export const DEFAULT_SORT: SortState = { column: "name", direction: "asc" };

/**
 * Parse a `lastModified` value into a comparable timestamp.
 *
 * `types.ts` types this field as `Date`, but the API sends it as JSON and
 * nothing parses it — at runtime it is a `string` (or `null`, since it is a
 * pointer on the Go side). `new Date(v).getTime()` on a genuine `Date` works
 * too, so this is safe regardless of which shape actually shows up. A `NaN`
 * result (missing/unparsable value) is normalised to 0 so it never reaches a
 * comparator's return value — a stray NaN silently corrupts Array#sort.
 */
const lastModifiedTime = (value: unknown): number => {
  if (value == null) return 0;
  const time = new Date(value as unknown as string).getTime();
  return Number.isNaN(time) ? 0 : time;
};

/** `size` is a pointer on the Go side and can arrive as `null`. */
const sizeValue = (value: number | null | undefined): number =>
  typeof value === "number" && !Number.isNaN(value) ? value : 0;

/**
 * Sort objects for display. Purely client-side, over whatever pages have been
 * loaded so far — S3's ListObjectsV2 has no server-side sort, so this can
 * never be a full-bucket sort (see the partial-sort hint in object-list.tsx).
 *
 * Always returns a new array; never mutates `objects` in place, since the
 * input is a flatMap over query pages and mutating it would corrupt render
 * order elsewhere.
 */
export function sortObjects<
  T extends { objectKey: string; size: number; lastModified: Date }
>(objects: T[], sort: SortState): T[] {
  const dir = sort.direction === "asc" ? 1 : -1;

  return [...objects].sort((a, b) => {
    let cmp = 0;

    if (sort.column === "name") {
      cmp = a.objectKey.localeCompare(b.objectKey);
    } else if (sort.column === "size") {
      cmp = sizeValue(a.size) - sizeValue(b.size);
    } else {
      cmp = lastModifiedTime(a.lastModified) - lastModifiedTime(b.lastModified);
    }

    if (cmp !== 0) return cmp * dir;

    // Ties break by objectKey ascending, regardless of direction, so equal
    // sizes/dates render in a stable, predictable order.
    return a.objectKey.localeCompare(b.objectKey);
  });
}

/**
 * Folders sort among themselves by name and always render above files,
 * regardless of the active column — a folder has neither a size nor a date, so
 * interleaving them with files would put blank rows in the middle of a sort.
 *
 * Honours only `direction`, and callers should only pass a non-default
 * direction when the active sort column is `name` — pass `"asc"` for any
 * other active column so prefixes render name-ascending regardless of what
 * the object table is sorted by.
 */
export function sortPrefixes(
  prefixes: string[],
  direction: SortDirection
): string[] {
  const dir = direction === "desc" ? -1 : 1;
  return [...prefixes].sort((a, b) => a.localeCompare(b) * dir);
}
