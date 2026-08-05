import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Menu, { MenuItem, type MenuProps } from "./menu";

/**
 * jsdom has no layout engine: every `getBoundingClientRect()` is 0×0 and
 * `documentElement.clientWidth/clientHeight` are 0. Floating UI therefore
 * measures a zero-size viewport, and `hide()` — correctly, given those numbers
 * — reports *every* reference as fully clipped. Without the stubs below the
 * surface would be `visibility: hidden` in every test here, which says nothing
 * about the component and hides it from `getByRole` as well.
 *
 * So these hand Floating UI the minimum layout a browser would give it: a
 * 1024×768 viewport, a trigger with a real box near the right edge (where a
 * table's row-action menu actually sits), a modest box for the surface, and a
 * full-viewport rect for everything else — otherwise the `overflow: hidden`
 * wrapper below would itself be a zero-size clipping ancestor.
 */
const VIEWPORT_WIDTH = 1024;
const VIEWPORT_HEIGHT = 768;
const TRIGGER_WIDTH = 48;

type Box = { x: number; y: number; width: number; height: number };

/** A trigger sitting comfortably inside the viewport. */
const VISIBLE_TRIGGER: Box = { x: 900, y: 40, width: TRIGGER_WIDTH, height: 32 };
/** The same trigger after the scroll container it lives in scrolled it away. */
const SCROLLED_AWAY_TRIGGER: Box = { ...VISIBLE_TRIGGER, y: -400 };

const toDOMRect = ({ x, y, width, height }: Box): DOMRect =>
  ({
    x,
    y,
    width,
    height,
    top: y,
    left: x,
    right: x + width,
    bottom: y + height,
    toJSON: () => ({}),
  }) as DOMRect;

const realGetBoundingClientRect = Element.prototype.getBoundingClientRect;
const realClientSize = (["clientWidth", "clientHeight"] as const).map(
  (prop) =>
    [prop, Object.getOwnPropertyDescriptor(Element.prototype, prop)] as const
);

const stubLayout = (trigger: Box) => {
  Element.prototype.getBoundingClientRect = function (this: Element) {
    if (this.getAttribute("aria-haspopup") === "menu") return toDOMRect(trigger);
    if (this.getAttribute("role") === "menu") {
      return toDOMRect({ x: 0, y: 0, width: 200, height: 160 });
    }
    return toDOMRect({
      x: 0,
      y: 0,
      width: VIEWPORT_WIDTH,
      height: VIEWPORT_HEIGHT,
    });
  };
};

beforeEach(() => {
  stubLayout(VISIBLE_TRIGGER);
  // Floating UI measures clipping ancestors — and the viewport itself — with
  // `clientWidth`/`clientHeight`, not `getBoundingClientRect()`. jsdom answers
  // 0 to both, which collapses the clipping rect to a point and makes every
  // reference "fully clipped". Deriving them from the rects above keeps the
  // two measurements consistent.
  for (const [prop] of realClientSize) {
    Object.defineProperty(Element.prototype, prop, {
      configurable: true,
      get(this: Element) {
        const rect = this.getBoundingClientRect();
        return prop === "clientWidth" ? rect.width : rect.height;
      },
    });
  }
});

afterEach(() => {
  Element.prototype.getBoundingClientRect = realGetBoundingClientRect;
  for (const [prop, descriptor] of realClientSize) {
    if (descriptor) Object.defineProperty(Element.prototype, prop, descriptor);
  }
});

/**
 * Renders a menu inside a clipping container, which is the shape every real
 * call site has: a table wrapped in `overflow-x-auto` / `overflow-y-hidden`.
 */
const renderMenu = (
  items?: React.ReactNode,
  props?: Partial<Omit<MenuProps, "trigger" | "children">>
) =>
  render(
    <div style={{ overflow: "hidden" }} data-testid="clip">
      <Menu trigger="Actions" placement="bottom-end" {...props}>
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

  it("keeps the surface visible while the trigger is on screen", async () => {
    const user = userEvent.setup();
    renderMenu();

    const menu = await openMenu(user);

    await waitFor(() => expect(menu).toHaveStyle({ visibility: "visible" }));
  });

  it("hides the surface once the trigger has scrolled out of view", async () => {
    // The reference is above the viewport, i.e. the table scrolled it away.
    // Portalling the surface removed the clipping ancestor that used to hide
    // it, so without `hide()` it would keep floating over unrelated content.
    stubLayout(SCROLLED_AWAY_TRIGGER);
    const user = userEvent.setup();
    renderMenu();

    await user.click(screen.getByRole("button", { name: "Actions" }));
    // `hidden: true` because a `visibility: hidden` surface is — correctly —
    // no longer part of the accessibility tree.
    const menu = await screen.findByRole("menu", { hidden: true });

    await waitFor(() => expect(menu).toHaveStyle({ visibility: "hidden" }));
    // Still mounted, not `display: none`: the box has to survive so
    // `autoUpdate` keeps measuring it and focus management keeps working.
    expect(menu).toBeInTheDocument();
    expect(menu.style.display).toBe("");
  });

  it("sizes the surface to its longest item, capped at the viewport", async () => {
    const user = userEvent.setup();
    renderMenu(
      <>
        <MenuItem onClick={() => {}}>Reset password</MenuItem>
        <MenuItem onClick={() => {}}>Delete</MenuItem>
      </>
    );

    const menu = await openMenu(user);

    await waitFor(() => {
      // Intrinsic: grows to the longest item instead of a fixed `w-*` class.
      expect(menu.style.width).toBe("max-content");
      // Floor: never narrower than the trigger that was clicked.
      expect(menu.style.minWidth).toBe(`${TRIGGER_WIDTH}px`);
      // Cap: the space the viewport actually leaves.
      const maxWidth = Number.parseFloat(menu.style.maxWidth);
      expect(maxWidth).toBeGreaterThan(0);
      expect(maxWidth).toBeLessThanOrEqual(VIEWPORT_WIDTH);
    });
  });

  it("matches the trigger width exactly when matchTriggerWidth is set", async () => {
    const user = userEvent.setup();
    renderMenu(undefined, { matchTriggerWidth: true });

    const menu = await openMenu(user);

    await waitFor(() => {
      expect(menu.style.width).toBe(`${TRIGGER_WIDTH}px`);
      expect(menu.style.minWidth).toBe(`${TRIGGER_WIDTH}px`);
    });
  });
});
