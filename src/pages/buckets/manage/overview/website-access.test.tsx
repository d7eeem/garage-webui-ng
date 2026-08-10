import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import WebsiteAccessSection from "./overview-website-access";

// `vi.mock` is hoisted above the imports, so the mutable state it closes
// over has to be hoisted with it — same pattern as account-button.test.tsx.
const mockUpdateMutation = vi.hoisted(() => ({
  mutate: vi.fn(),
  isPending: false,
  isSuccess: false,
  isError: false,
}));

const mockBucket = vi.hoisted(() => ({
  bucket: {
    id: "bucket-1",
    websiteAccess: false as boolean,
    websiteConfig: null as { indexDocument: string; errorDocument: string } | null,
  },
  bucketName: "my-bucket",
}));

vi.mock("../context", () => ({
  useBucketContext: () => mockBucket,
}));

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canWrite: true }),
}));

vi.mock("@/hooks/useConfig", () => ({
  useConfig: () => ({ data: {} }),
}));

vi.mock("../hooks", () => ({
  useUpdateBucket: () => mockUpdateMutation,
  useObjectExists: () => ({ presence: "unknown", isLoading: false }),
}));

// The "info" link button renders react-router's <Link>, which needs a
// router context even though this test never navigates.
const renderSection = () =>
  render(
    <MemoryRouter>
      <WebsiteAccessSection />
    </MemoryRouter>
  );

describe("WebsiteAccessSection", () => {
  beforeEach(() => {
    mockUpdateMutation.mutate.mockClear();
    mockBucket.bucket = {
      id: "bucket-1",
      websiteAccess: false,
      websiteConfig: null,
    };
  });

  // These first three are the point of plan 039: enabling public access must
  // be interceptable, not fired straight off the toggle.

  it("enabling opens the confirm modal and does NOT call the mutation", async () => {
    const user = userEvent.setup();
    renderSection();

    await user.click(screen.getByRole("checkbox"));

    expect(
      screen.getByText(/enable public access\?/i)
    ).toBeInTheDocument();
    expect(mockUpdateMutation.mutate).not.toHaveBeenCalled();
  });

  it("confirming calls the mutation with enabled: true", async () => {
    const user = userEvent.setup();
    renderSection();

    await user.click(screen.getByRole("checkbox"));
    await user.click(
      screen.getByRole("button", { name: /enable public access/i })
    );

    expect(mockUpdateMutation.mutate).toHaveBeenCalledTimes(1);
    expect(mockUpdateMutation.mutate).toHaveBeenCalledWith({
      websiteAccess: expect.objectContaining({ enabled: true }),
    });
  });

  it("cancelling calls nothing and leaves the toggle OFF", async () => {
    const user = userEvent.setup();
    renderSection();

    const toggle = screen.getByRole("checkbox") as HTMLInputElement;
    await user.click(toggle);
    await user.click(screen.getByRole("button", { name: /cancel/i }));

    expect(mockUpdateMutation.mutate).not.toHaveBeenCalled();
    expect(toggle.checked).toBe(false);
    // react-daisyui's Modal keeps its content mounted even when closed (only
    // the <dialog>'s `open` attribute toggles), so assert on that attribute
    // rather than presence/absence of the text.
    expect(document.querySelector("dialog[open]")).toBeNull();
  });

  it("disabling calls the mutation with enabled: false and shows no modal", async () => {
    mockBucket.bucket = {
      id: "bucket-1",
      websiteAccess: true,
      websiteConfig: {
        indexDocument: "index.html",
        errorDocument: "error/400.html",
      },
    };
    const user = userEvent.setup();
    renderSection();

    const toggle = screen.getByRole("checkbox") as HTMLInputElement;
    expect(toggle.checked).toBe(true);

    await user.click(toggle);

    expect(mockUpdateMutation.mutate).toHaveBeenCalledTimes(1);
    expect(mockUpdateMutation.mutate).toHaveBeenCalledWith({
      websiteAccess: { enabled: false },
    });
    expect(document.querySelector("dialog[open]")).toBeNull();
  });

  it("editing the index document still auto-saves (debounced path untouched)", async () => {
    vi.useFakeTimers();
    mockBucket.bucket = {
      id: "bucket-1",
      websiteAccess: true,
      websiteConfig: {
        indexDocument: "index.html",
        errorDocument: "error/400.html",
      },
    };

    renderSection();

    const input = screen.getByDisplayValue("index.html");
    fireEvent.change(input, { target: { value: "home.html" } });

    vi.advanceTimersByTime(500);

    expect(mockUpdateMutation.mutate).toHaveBeenCalledWith({
      websiteAccess: expect.objectContaining({
        enabled: true,
        indexDocument: "home.html",
      }),
    });

    vi.useRealTimers();
  });
});
