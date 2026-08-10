import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import UploadCard from "./upload-card";
import type { UploadItem } from "@/pages/buckets/manage/browse/types";

// `vi.mock` is hoisted above the imports, so the mutable state it closes
// over has to be hoisted with it — same pattern as account-button.test.tsx
// and website-access.test.tsx.
const mockQueue = vi.hoisted(() => ({
  items: [] as UploadItem[],
  collapsed: false,
  completed: null as { bucket: string; seq: number } | null,
}));

const mockActions = vi.hoisted(() => ({
  toggleCollapsed: vi.fn(),
  clearFinished: vi.fn(),
  cancel: vi.fn(),
  retry: vi.fn(),
}));

vi.mock("@/pages/buckets/manage/browse/upload-queue", () => ({
  __esModule: true,
  default: mockActions,
  useUploadQueue: () => mockQueue,
}));

const mockBuckets = vi.hoisted(() => ({
  data: [] as { globalAliases: string[]; websiteAccess: boolean }[],
}));

vi.mock("@/pages/buckets/hooks", () => ({
  useBuckets: () => ({ data: mockBuckets.data }),
}));

vi.mock("@/hooks/useConfig", () => ({
  useConfig: () => ({ data: {} }),
}));

const mockGetPublicAccess = vi.hoisted(() => vi.fn());
vi.mock("@/lib/website", () => ({
  getPublicAccess: (...args: unknown[]) => mockGetPublicAccess(...args),
}));

const makeItem = (overrides: Partial<UploadItem>): UploadItem => ({
  id: "item-1",
  key: "prefix/file.txt",
  name: "file.txt",
  bucket: "my-bucket",
  size: 100,
  loaded: 0,
  status: "queued",
  ...overrides,
});

const renderCard = () =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <UploadCard />
    </QueryClientProvider>
  );

describe("UploadCard", () => {
  beforeEach(() => {
    mockQueue.items = [];
    mockQueue.collapsed = false;
    mockQueue.completed = null;
    mockActions.toggleCollapsed.mockClear();
    mockActions.clearFinished.mockClear();
    mockActions.cancel.mockClear();
    mockActions.retry.mockClear();
    mockBuckets.data = [];
    mockGetPublicAccess.mockReset();
  });

  it("renders nothing when the queue is empty", () => {
    mockQueue.items = [];
    const { container } = renderCard();

    expect(container).toBeEmptyDOMElement();
  });

  it("renders one card with a row per item", () => {
    mockQueue.items = [
      makeItem({ id: "a", name: "a.txt", status: "queued" }),
      makeItem({ id: "b", name: "b.txt", status: "uploading", loaded: 50 }),
      makeItem({ id: "c", name: "c.txt", status: "canceled" }),
    ];
    const { container } = renderCard();

    expect(screen.getByText("Uploads")).toBeInTheDocument();
    expect(container.querySelectorAll("li")).toHaveLength(3);
  });

  it("shows [Copy URL] for a public bucket, Private for a private one, and neither for an unknown bucket", () => {
    mockQueue.items = [
      makeItem({ id: "pub", name: "public.txt", bucket: "pub-bucket", status: "done" }),
      makeItem({ id: "priv", name: "private.txt", bucket: "priv-bucket", status: "done" }),
      makeItem({ id: "unk", name: "unknown.txt", bucket: "ghost-bucket", status: "done" }),
    ];
    mockBuckets.data = [
      { globalAliases: ["pub-bucket"], websiteAccess: true },
      { globalAliases: ["priv-bucket"], websiteAccess: false },
      // "ghost-bucket" intentionally absent: simulates a bucket not found
      // in the buckets-list cache.
    ];
    mockGetPublicAccess.mockImplementation((websiteAccess: boolean) =>
      websiteAccess
        ? { state: "public", url: "https://pub-bucket.web.example/public.txt" }
        : { state: "private" }
    );

    renderCard();

    expect(
      screen.getByRole("button", { name: /copy public url for public\.txt/i })
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", {
        name: /copy public url for private\.txt/i,
      })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", {
        name: /copy public url for unknown\.txt/i,
      })
    ).not.toBeInTheDocument();

    // Exactly one "Private" label — for the known-private bucket only, not
    // for the unknown one (never guess public *or* private).
    expect(screen.getAllByText("Private")).toHaveLength(1);
  });

  it("[Retry] calls uploadQueue.retry with the item's id", async () => {
    const user = userEvent.setup();
    mockQueue.items = [
      makeItem({
        id: "failed-1",
        name: "broken.txt",
        status: "error",
        error: "boom",
      }),
    ];
    renderCard();

    await user.click(
      screen.getByRole("button", { name: /retry upload of broken\.txt/i })
    );

    expect(mockActions.retry).toHaveBeenCalledTimes(1);
    expect(mockActions.retry).toHaveBeenCalledWith("failed-1");
  });
});
