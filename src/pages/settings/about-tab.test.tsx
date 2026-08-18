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
        deployment?: "binary" | "managed" | "unknown";
        updateCommand?: string;
        canSelfUpdate?: boolean;
      }
    | undefined,
}));

vi.mock("@/hooks/useConfig", () => ({
  useConfig: () => ({ data: mockData.config }),
}));

const mockApplyUpdate = vi.hoisted(() => ({
  mutate: vi.fn(),
  isPending: false,
}));

vi.mock("./hooks", () => ({
  useUpdateCheck: () => ({ data: mockData.update }),
  useApplyUpdate: () => mockApplyUpdate,
}));

const mockCopyToClipboard = vi.hoisted(() => vi.fn());

vi.mock("@/lib/utils", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/utils")>();
  return {
    ...actual,
    copyToClipboard: mockCopyToClipboard,
  };
});

describe("AboutTab", () => {
  beforeEach(() => {
    mockData.config = undefined;
    mockData.update = undefined;
    mockCopyToClipboard.mockClear();
    mockApplyUpdate.mutate.mockClear();
    mockApplyUpdate.isPending = false;
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

  it("renders the update command when present", () => {
    mockData.config = { version: "v3.3.0" };
    mockData.update = {
      enabled: false,
      current: "v3.3.0",
      deployment: "binary",
      updateCommand:
        "sudo systemctl stop garage-webui && sudo install -m 0755 ./garage-webui-ng /usr/local/bin/garage-webui-ng && sudo systemctl start garage-webui",
    };

    render(<AboutTab />);

    expect(
      screen.getByText(/Download the release binary first, then/)
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "sudo systemctl stop garage-webui && sudo install -m 0755 ./garage-webui-ng /usr/local/bin/garage-webui-ng && sudo systemctl start garage-webui"
      )
    ).toBeInTheDocument();
  });

  it("renders no command block when updateCommand is absent or empty", () => {
    mockData.config = { version: "v3.3.0" };
    mockData.update = {
      enabled: false,
      current: "v3.3.0",
      deployment: "unknown",
      updateCommand: "",
    };

    render(<AboutTab />);

    expect(
      screen.queryByText(/Download the release binary first/)
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/updated from outside the app/)
    ).not.toBeInTheDocument();
  });

  it("renders update-from-outside prose (no code block, no copy button) when deployment is managed", () => {
    mockData.config = { version: "v3.3.0" };
    mockData.update = {
      enabled: false,
      current: "v3.3.0",
      deployment: "managed",
      updateCommand: "",
    };

    render(<AboutTab />);

    expect(
      screen.getByText(/updated from outside the app/)
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Copy update command")).not.toBeInTheDocument();
    expect(document.querySelector("code")).not.toBeInTheDocument();
  });

  it("never suggests a docker command for a managed deployment", () => {
    mockData.config = { version: "v3.3.0" };
    mockData.update = {
      enabled: false,
      current: "v3.3.0",
      deployment: "managed",
      updateCommand: "",
    };

    render(<AboutTab />);

    expect(document.body.textContent).not.toContain("docker");
  });

  it("copies the exact update command when the copy button is clicked", async () => {
    const { default: userEvent } = await import("@testing-library/user-event");
    mockData.config = { version: "v3.3.0" };
    mockData.update = {
      enabled: false,
      current: "v3.3.0",
      deployment: "binary",
      updateCommand:
        "sudo systemctl stop garage-webui && sudo install -m 0755 ./garage-webui-ng /usr/local/bin/garage-webui-ng && sudo systemctl start garage-webui",
    };

    render(<AboutTab />);

    const user = userEvent.setup();
    await user.click(screen.getByLabelText("Copy update command"));

    expect(mockCopyToClipboard).toHaveBeenCalledWith(
      "sudo systemctl stop garage-webui && sudo install -m 0755 ./garage-webui-ng /usr/local/bin/garage-webui-ng && sudo systemctl start garage-webui"
    );
  });

  describe("in-browser update (POST /update/apply)", () => {
    it("shows no Update now button when canSelfUpdate is false", () => {
      mockData.config = { version: "v3.3.0" };
      mockData.update = {
        enabled: true,
        current: "v3.3.0",
        latest: "v3.4.0",
        updateAvailable: true,
        canSelfUpdate: false,
      };

      render(<AboutTab />);

      expect(
        screen.queryByRole("button", { name: /update now/i })
      ).not.toBeInTheDocument();
    });

    it("shows no Update now button when canSelfUpdate is absent", () => {
      mockData.config = { version: "v3.3.0" };
      mockData.update = {
        enabled: true,
        current: "v3.3.0",
        latest: "v3.4.0",
        updateAvailable: true,
      };

      render(<AboutTab />);

      expect(
        screen.queryByRole("button", { name: /update now/i })
      ).not.toBeInTheDocument();
    });

    it("shows the Update now button when canSelfUpdate and updateAvailable are both true", () => {
      mockData.config = { version: "v3.3.0" };
      mockData.update = {
        enabled: true,
        current: "v3.3.0",
        latest: "v3.4.0",
        updateAvailable: true,
        canSelfUpdate: true,
      };

      render(<AboutTab />);

      expect(
        screen.getByRole("button", { name: /update now/i })
      ).toBeInTheDocument();
    });

    it("clicking Update now, confirmed, calls the mutation with restart: false by default", async () => {
      const { default: userEvent } = await import("@testing-library/user-event");
      mockData.config = { version: "v3.3.0" };
      mockData.update = {
        enabled: true,
        current: "v3.3.0",
        latest: "v3.4.0",
        updateAvailable: true,
        canSelfUpdate: true,
      };
      vi.spyOn(window, "confirm").mockReturnValue(true);

      render(<AboutTab />);

      const user = userEvent.setup();
      await user.click(screen.getByRole("button", { name: /update now/i }));

      expect(window.confirm).toHaveBeenCalled();
      expect(mockApplyUpdate.mutate).toHaveBeenCalledWith({ restart: false });
    });

    it("sends restart: true when the restart checkbox is checked first", async () => {
      const { default: userEvent } = await import("@testing-library/user-event");
      mockData.config = { version: "v3.3.0" };
      mockData.update = {
        enabled: true,
        current: "v3.3.0",
        latest: "v3.4.0",
        updateAvailable: true,
        canSelfUpdate: true,
      };
      vi.spyOn(window, "confirm").mockReturnValue(true);

      render(<AboutTab />);

      const user = userEvent.setup();
      await user.click(
        screen.getByLabelText(
          /restart the service automatically after installing/i
        )
      );
      await user.click(screen.getByRole("button", { name: /update now/i }));

      expect(mockApplyUpdate.mutate).toHaveBeenCalledWith({ restart: true });
    });

    it("calls nothing when the confirmation is declined", async () => {
      const { default: userEvent } = await import("@testing-library/user-event");
      mockData.config = { version: "v3.3.0" };
      mockData.update = {
        enabled: true,
        current: "v3.3.0",
        latest: "v3.4.0",
        updateAvailable: true,
        canSelfUpdate: true,
      };
      vi.spyOn(window, "confirm").mockReturnValue(false);

      render(<AboutTab />);

      const user = userEvent.setup();
      await user.click(screen.getByRole("button", { name: /update now/i }));

      expect(window.confirm).toHaveBeenCalled();
      expect(mockApplyUpdate.mutate).not.toHaveBeenCalled();
    });
  });
});
