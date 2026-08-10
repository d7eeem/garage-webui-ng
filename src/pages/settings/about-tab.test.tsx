import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AboutTab from "./about-tab";

// `vi.mock` is hoisted above the imports, so the mutable state it closes over
// has to be hoisted with it.
const mockData = vi.hoisted(() => ({
  config: undefined as { version?: string } | undefined,
  update: undefined as
    | {
        enabled: boolean;
        current: string;
        latest?: string;
        url?: string;
        updateAvailable?: boolean;
        checkFailed?: boolean;
      }
    | undefined,
}));

vi.mock("@/hooks/useConfig", () => ({
  useConfig: () => ({ data: mockData.config }),
}));

vi.mock("./hooks", () => ({
  useUpdateCheck: () => ({ data: mockData.update }),
}));

describe("AboutTab", () => {
  beforeEach(() => {
    mockData.config = undefined;
    mockData.update = undefined;
  });

  it("renders the version from config", () => {
    mockData.config = { version: "v3.3.0" };

    render(<AboutTab />);

    expect(screen.getByText("v3.3.0")).toBeInTheDocument();
  });

  it("renders unknown when config has no version", () => {
    mockData.config = {};

    render(<AboutTab />);

    expect(screen.getByText("unknown")).toBeInTheDocument();
  });

  it("shows the update line when an update is available, with a link to the release", () => {
    mockData.config = { version: "v3.3.0" };
    mockData.update = {
      enabled: true,
      current: "v3.3.0",
      latest: "v3.4.0",
      url: "https://github.com/d7eeem/garage-webui-ng/releases/tag/v3.4.0",
      updateAvailable: true,
    };

    render(<AboutTab />);

    expect(screen.getByText(/Update available: v3\.4\.0/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /view release/i })).toHaveAttribute(
      "href",
      "https://github.com/d7eeem/garage-webui-ng/releases/tag/v3.4.0"
    );
  });

  it("shows no update line when no update is available", () => {
    mockData.config = { version: "v3.3.0" };
    mockData.update = {
      enabled: true,
      current: "v3.3.0",
      latest: "v3.3.0",
      updateAvailable: false,
    };

    render(<AboutTab />);

    expect(screen.queryByText(/Update available/)).not.toBeInTheDocument();
  });

  it("shows the disabled hint when update checks are off", () => {
    mockData.config = { version: "v3.3.0" };
    mockData.update = { enabled: false, current: "v3.3.0" };

    render(<AboutTab />);

    expect(screen.getByText(/Update checks are off/)).toBeInTheDocument();
  });
});
