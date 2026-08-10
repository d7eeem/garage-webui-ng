import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ObjectList from "./object-list";
import { GetObjectsResult } from "./types";

// `vi.mock` is hoisted above the imports, so the mutable state it closes over
// has to be hoisted with it. Modeled on
// src/components/containers/account-button.test.tsx.
const mockBrowse = vi.hoisted(() => ({
  data: undefined as { pages: GetObjectsResult[] } | undefined,
  error: null as Error | null,
  isLoading: false,
  hasNextPage: false,
}));

vi.mock("./hooks", () => ({
  useBrowseObjects: () => ({
    data: mockBrowse.data,
    error: mockBrowse.error,
    isLoading: mockBrowse.isLoading,
    fetchNextPage: vi.fn(),
    hasNextPage: mockBrowse.hasNextPage,
    isFetchingNextPage: false,
  }),
  // object-actions.tsx also imports from "./hooks" (same module id) — it must
  // be mocked here too or the mock factory below is incomplete.
  useDeleteObject: () => ({ mutate: vi.fn() }),
}));

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canWrite: true }),
}));

vi.mock("@/hooks/useConfig", () => ({
  useConfig: () => ({ data: undefined }),
}));

vi.mock("../context", () => ({
  useBucketContext: () => ({
    bucket: { websiteAccess: false },
    bucketName: "test-bucket",
    refetch: vi.fn(),
  }),
}));

const renderList = () => {
  const queryClient = new QueryClient();
  return render(
    <QueryClientProvider client={queryClient}>
      <ObjectList selected={new Set()} setSelected={vi.fn()} />
    </QueryClientProvider>
  );
};

/** Sum of a row's `<td>` widths, accounting for `colSpan` (default 1). */
const effectiveCells = (row: HTMLTableRowElement) =>
  Array.from(row.querySelectorAll("td")).reduce(
    (n, td) => n + (Number(td.getAttribute("colspan")) || 1),
    0
  );

const oneObjectPage = (): { pages: GetObjectsResult[] } => ({
  pages: [
    {
      prefixes: [],
      objects: [
        {
          objectKey: "report.pdf",
          lastModified: new Date("2026-01-01T00:00:00Z"),
          size: 1234,
          url: "/report.pdf",
        },
      ],
      prefix: "",
      nextToken: null,
    },
  ],
});

const onePrefixPage = (): { pages: GetObjectsResult[] } => ({
  pages: [
    {
      prefixes: ["folder/"],
      objects: [],
      prefix: "",
      nextToken: null,
    },
  ],
});

// Names are deliberately ordered opposite to size, so that a size sort
// produces a different row order than the default (name-ascending) one —
// otherwise a test could pass by accident without the sort actually running.
const multiObjectPage = (): { pages: GetObjectsResult[] } => ({
  pages: [
    {
      prefixes: [],
      objects: [
        {
          objectKey: "b-medium.bin",
          lastModified: new Date("2026-01-02T00:00:00Z"),
          size: 500,
          url: "/b-medium.bin",
        },
        {
          objectKey: "a-huge.bin",
          lastModified: new Date("2026-01-01T00:00:00Z"),
          size: 900,
          url: "/a-huge.bin",
        },
        {
          objectKey: "c-tiny.bin",
          lastModified: new Date("2026-01-03T00:00:00Z"),
          size: 100,
          url: "/c-tiny.bin",
        },
      ],
      prefix: "",
      nextToken: null,
    },
  ],
});

/** Object names in the order the rows currently render, top to bottom. */
const renderedObjectNames = () =>
  screen
    .getAllByRole("row")
    .map((row) => row.querySelector("td:nth-child(2)"))
    .filter((cell): cell is HTMLTableCellElement => cell !== null)
    .map((cell) => cell.textContent?.trim())
    .filter((text): text is string => !!text && text !== "Actions");

describe("ObjectList", () => {
  beforeEach(() => {
    mockBrowse.data = undefined;
    mockBrowse.error = null;
    mockBrowse.isLoading = false;
    mockBrowse.hasNextPage = false;
  });

  it("declares as many header columns as a data row renders", () => {
    mockBrowse.data = oneObjectPage();
    renderList();

    const headerCount = screen.getAllByRole("columnheader").length;
    const dataRow = screen.getByText("report").closest("tr");
    expect(dataRow).not.toBeNull();
    expect(effectiveCells(dataRow as HTMLTableRowElement)).toBe(headerCount);
  });

  it("gives the prefix (folder) row the same effective width as the header", () => {
    mockBrowse.data = onePrefixPage();
    renderList();

    const headerCount = screen.getAllByRole("columnheader").length;
    const prefixRow = screen.getByText("folder").closest("tr");
    expect(prefixRow).not.toBeNull();
    expect(effectiveCells(prefixRow as HTMLTableRowElement)).toBe(headerCount);
  });

  it("spans the full table width in the empty state", () => {
    mockBrowse.data = { pages: [{ prefixes: [], objects: [], prefix: "", nextToken: null }] };
    renderList();

    const headerCount = screen.getAllByRole("columnheader").length;
    const emptyCell = screen.getByText("No objects").closest("td");
    expect(emptyCell).not.toBeNull();
    expect(Number(emptyCell?.getAttribute("colspan"))).toBe(headerCount);
  });

  it("names the actions column for assistive technology", () => {
    mockBrowse.data = oneObjectPage();
    renderList();

    expect(
      screen.getByRole("columnheader", { name: "Actions" })
    ).toBeInTheDocument();
  });

  it("reorders the rendered rows when the Size header is clicked", () => {
    mockBrowse.data = multiObjectPage();
    renderList();

    // Default order is server order (name ascending): a-huge, b-medium, c-tiny.
    expect(renderedObjectNames()).toEqual([
      "a-huge.bin",
      "b-medium.bin",
      "c-tiny.bin",
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Sort by size" }));

    // Ascending by size: c-tiny (100), b-medium (500), a-huge (900).
    expect(renderedObjectNames()).toEqual([
      "c-tiny.bin",
      "b-medium.bin",
      "a-huge.bin",
    ]);
  });

  it("reverses the order when the same header is clicked twice", () => {
    mockBrowse.data = multiObjectPage();
    renderList();

    const sizeHeader = screen.getByRole("button", { name: "Sort by size" });
    fireEvent.click(sizeHeader); // ascending: c-tiny, b-medium, a-huge
    fireEvent.click(sizeHeader); // descending: a-huge, b-medium, c-tiny

    expect(renderedObjectNames()).toEqual([
      "a-huge.bin",
      "b-medium.bin",
      "c-tiny.bin",
    ]);
  });

  it("still declares as many header columns as a data row renders after sorting", () => {
    mockBrowse.data = multiObjectPage();
    renderList();

    fireEvent.click(screen.getByRole("button", { name: "Sort by size" }));

    const headerCount = screen.getAllByRole("columnheader").length;
    const dataRow = screen.getByText("a-huge").closest("tr");
    expect(dataRow).not.toBeNull();
    expect(effectiveCells(dataRow as HTMLTableRowElement)).toBe(headerCount);
  });

  it("shows the partial-sort hint when more pages remain and a non-default sort is active", () => {
    mockBrowse.data = multiObjectPage();
    mockBrowse.hasNextPage = true;
    renderList();

    expect(
      screen.queryByText(/Sorted across the objects loaded so far/)
    ).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Sort by size" }));

    expect(
      screen.getByText(/Sorted across the objects loaded so far/)
    ).toBeInTheDocument();
  });

  it("hides the partial-sort hint when there is no next page, even with a non-default sort", () => {
    mockBrowse.data = multiObjectPage();
    mockBrowse.hasNextPage = false;
    renderList();

    fireEvent.click(screen.getByRole("button", { name: "Sort by size" }));

    expect(
      screen.queryByText(/Sorted across the objects loaded so far/)
    ).toBeNull();
  });
});
