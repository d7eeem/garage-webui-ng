import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import Menu, { MenuItem } from "./menu";

/**
 * Renders a menu inside a clipping container, which is the shape every real
 * call site has: a table wrapped in `overflow-x-auto` / `overflow-y-hidden`.
 */
const renderMenu = (items?: React.ReactNode) =>
  render(
    <div style={{ overflow: "hidden" }} data-testid="clip">
      <Menu trigger="Actions" placement="bottom-end">
        {items ?? (
          <>
            <MenuItem onClick={() => {}}>Share</MenuItem>
            <MenuItem onClick={() => {}}>Delete</MenuItem>
          </>
        )}
      </Menu>
    </div>
  );

const openMenu = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(screen.getByRole("button", { name: "Actions" }));
  return screen.findByRole("menu");
};

describe("<Menu />", () => {
  it("renders the surface outside its overflow-hidden ancestor", async () => {
    const user = userEvent.setup();
    renderMenu();

    const menu = await openMenu(user);

    // The decisive assertion: a surface still inside the clipping container is
    // a surface that gets cut off, whatever its z-index or placement.
    expect(screen.getByTestId("clip")).not.toContainElement(menu);
    expect(document.body).toContainElement(menu);
  });

  it("opens on trigger click and marks the trigger expanded", async () => {
    const user = userEvent.setup();
    renderMenu();

    const trigger = screen.getByRole("button", { name: "Actions" });
    expect(trigger).toHaveAttribute("aria-haspopup", "menu");
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();

    await user.click(trigger);

    expect(await screen.findByRole("menu")).toBeInTheDocument();
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(screen.getAllByRole("menuitem")).toHaveLength(2);
  });

  it("closes on Escape and returns focus to the trigger", async () => {
    const user = userEvent.setup();
    renderMenu();

    await openMenu(user);
    await user.keyboard("{Escape}");

    await waitFor(() =>
      expect(screen.queryByRole("menu")).not.toBeInTheDocument()
    );
    expect(screen.getByRole("button", { name: "Actions" })).toHaveFocus();
  });

  it("closes on an outside click", async () => {
    const user = userEvent.setup();
    renderMenu();

    await openMenu(user);
    await user.click(document.body);

    await waitFor(() =>
      expect(screen.queryByRole("menu")).not.toBeInTheDocument()
    );
  });

  it("moves focus to the first item on ArrowDown", async () => {
    const user = userEvent.setup();
    renderMenu();

    await openMenu(user);
    await user.keyboard("{ArrowDown}");

    await waitFor(() =>
      expect(screen.getByRole("menuitem", { name: "Share" })).toHaveFocus()
    );
  });

  it("fires onClick and closes the menu", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    renderMenu(
      <>
        <MenuItem onClick={onClick}>Share</MenuItem>
        <MenuItem onClick={() => {}}>Delete</MenuItem>
      </>
    );

    await openMenu(user);
    await user.click(screen.getByRole("menuitem", { name: "Share" }));

    expect(onClick).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(screen.queryByRole("menu")).not.toBeInTheDocument()
    );
  });

  it("does not fire a disabled item and exposes its reason", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    renderMenu(
      <MenuItem
        onClick={onClick}
        disabled
        disabledReason="This is the last enabled administrator"
      >
        Delete
      </MenuItem>
    );

    await openMenu(user);
    const item = screen.getByRole("menuitem", { name: "Delete" });

    expect(item).toHaveAttribute("aria-disabled", "true");
    expect(item).toHaveAttribute(
      "title",
      "This is the last enabled administrator"
    );

    await user.click(item);

    expect(onClick).not.toHaveBeenCalled();
    expect(screen.getByRole("menu")).toBeInTheDocument();
  });
});
