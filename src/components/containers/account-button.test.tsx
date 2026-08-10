import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AccountButton from "./account-button";

// `vi.mock` is hoisted above the imports, so the mutable state it closes over
// has to be hoisted with it.
const mockAuth = vi.hoisted(() => ({
  username: undefined as string | undefined,
}));

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ username: mockAuth.username }),
}));

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <AccountButton />
    </MemoryRouter>
  );

describe("AccountButton", () => {
  beforeEach(() => {
    mockAuth.username = "abdulrahman";
  });

  it("links to /settings and shows the signed-in username", () => {
    renderAt("/");

    expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute(
      "href",
      "/settings"
    );
    expect(screen.getByText("abdulrahman")).toBeInTheDocument();
  });

  it("still renders a working Settings link before the username loads", () => {
    // `useAuth().username` is `string | undefined` — undefined on first paint,
    // while /auth/status is in flight. Settings must stay reachable: this chip
    // is the only route to it.
    mockAuth.username = undefined;
    renderAt("/");

    expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute(
      "href",
      "/settings"
    );
  });

  it("marks itself active on the settings route", () => {
    renderAt("/settings");

    expect(screen.getByRole("link", { name: /settings/i })).toHaveClass(
      "btn-active"
    );
  });

  it("is not active on an unrelated route", () => {
    renderAt("/buckets");

    expect(screen.getByRole("link", { name: /settings/i })).not.toHaveClass(
      "btn-active"
    );
  });
});
