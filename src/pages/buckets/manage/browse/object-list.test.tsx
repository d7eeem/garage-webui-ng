import { render, screen } from "@testing-library/react";
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
});
