import { cn } from "@/lib/utils";
import { Z_LAYERS } from "@/lib/z-layers";
import {
  autoUpdate,
  flip,
  FloatingFocusManager,
  FloatingList,
  FloatingPortal,
  hide,
  offset,
  shift,
  size,
  useClick,
  useDismiss,
  useFloating,
  useInteractions,
  useListItem,
  useListNavigation,
  useRole,
  useTransitionStyles,
  useTypeahead,
  type Placement,
} from "@floating-ui/react";
import { LucideIcon } from "lucide-react";
import {
  createContext,
  useContext,
  useMemo,
  useRef,
  useState,
  type HTMLProps,
  type ReactNode,
} from "react";

/**
 * A portal-based action menu.
 *
 * react-daisyui's `Dropdown` is pure CSS with no portal: the surface is a
 * sibling of the trigger, so every one of this app's menus sat inside a table's
 * `overflow-*` wrapper and was clipped by it. No z-index or placement value
 * escapes an `overflow` ancestor — only a portal does, which is what this
 * component is for.
 *
 * `autoUpdate` + `flip()`/`shift()` also replace the hand-written "is this one
 * of the last rows?" flip calculations the old call sites carried. If one of
 * those ever reappears, it is a regression.
 */

type MenuContextValue = {
  activeIndex: number | null;
  getItemProps: (
    props?: HTMLProps<HTMLElement>
  ) => Record<string, unknown>;
  close: () => void;
};

const MenuContext = createContext<MenuContextValue | null>(null);

const useMenuContext = () => {
  const context = useContext(MenuContext);
  if (!context) {
    throw new Error("<MenuItem> must be rendered inside a <Menu>");
  }
  return context;
};

export type MenuProps = {
  /** Rendered inside the reference button (icon and/or label). */
  trigger: ReactNode;
  /** `MenuItem` list. */
  children: ReactNode;
  placement?: Placement;
  offsetPx?: number;
  /** Stretch the surface to the trigger's width via `size()`. */
  matchTriggerWidth?: boolean;
  /** Extra classes on the floating surface. */
  className?: string;
  /** Classes for the reference button — defaults to daisyUI's ghost icon button. */
  triggerClassName?: string;
  /** Accessible name for an icon-only trigger. */
  triggerLabel?: string;
  /**
   * Escape hatch: portal into this element instead of `document.body`. Needed
   * only for a menu rendered inside a native `<dialog>`, which paints in the
   * browser's top layer above every z-index in `Z_LAYERS`. No current call site
   * needs it.
   */
  portalRoot?: HTMLElement | null;
};

/** Never let the surface collapse to nothing when the viewport is tiny. */
const MIN_SURFACE_HEIGHT = 120;

const Menu = ({
  trigger,
  children,
  placement = "bottom-end",
  offsetPx = 4,
  matchTriggerWidth = false,
  className,
  triggerClassName,
  triggerLabel,
  portalRoot,
}: MenuProps) => {
  const [isOpen, setIsOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState<number | null>(null);

  const elementsRef = useRef<Array<HTMLElement | null>>([]);
  const labelsRef = useRef<Array<string | null>>([]);

  const { refs, floatingStyles, context, middlewareData } = useFloating({
    open: isOpen,
    onOpenChange: setIsOpen,
    placement,
    whileElementsMounted: autoUpdate,
    middleware: [
      offset(offsetPx),
      flip({ padding: 8 }),
      shift({ padding: 8 }),
      size({
        padding: 8,
        apply({ availableWidth, availableHeight, rects, elements }) {
          const surface = elements.floating;

          // Width rules, in order:
          //  • never narrower than the trigger the user clicked,
          //  • otherwise intrinsic — `max-content` grows the surface to its
          //    longest item instead of truncating it against a fixed class,
          //  • never wider than the space the viewport actually leaves.
          // `matchTriggerWidth` stays an opt-in *exact* match for select-style
          // callers; the default is intrinsic.
          Object.assign(surface.style, {
            minWidth: matchTriggerWidth
              ? `${rects.reference.width}px`
              : `${Math.min(rects.reference.width, availableWidth)}px`,
            width: matchTriggerWidth
              ? `${rects.reference.width}px`
              : "max-content",
            maxWidth: `${availableWidth}px`,
          });

          // Clamp to the space actually available, but never *raise* a cap a
          // caller set with a class (the theme picker's `max-h-[500px]`), since
          // an inline style would silently win over it. Reading the computed
          // value with our own inline value cleared is the only way to tell the
          // two apart.
          surface.style.removeProperty("max-height");
          const fromClass = Number.parseFloat(
            window.getComputedStyle(surface).maxHeight
          );
          const cap = Number.isFinite(fromClass)
            ? Math.min(fromClass, availableHeight)
            : availableHeight;

          surface.style.maxHeight = `${Math.max(
            MIN_SURFACE_HEIGHT,
            Math.floor(cap)
          )}px`;
        },
      }),
      // Last on purpose: hide() reports on the *final* computed position, so it
      // has to run after everything that can still move the surface. When the
      // trigger scrolls out of its container the portal surface would otherwise
      // keep floating over unrelated content — portalling it removed the
      // clipping ancestor that used to hide it, so we hide it explicitly.
      hide({ padding: 8 }),
    ],
  });

  // `visibility`, never `display: none`: the surface must keep its box so
  // `autoUpdate` keeps measuring it and focus management keeps working while
  // it is out of view.
  const isReferenceHidden = middlewareData.hide?.referenceHidden === true;

  const click = useClick(context);
  const dismiss = useDismiss(context);
  const role = useRole(context, { role: "menu" });
  const listNavigation = useListNavigation(context, {
    listRef: elementsRef,
    activeIndex,
    onNavigate: setActiveIndex,
    loop: true,
  });
  const typeahead = useTypeahead(context, {
    listRef: labelsRef,
    activeIndex,
    onMatch: setActiveIndex,
    enabled: isOpen,
  });

  const { getReferenceProps, getFloatingProps, getItemProps } = useInteractions([
    click,
    dismiss,
    role,
    listNavigation,
    typeahead,
  ]);

  // A short fade/scale. It lives on an inner element because `floatingStyles`
  // already owns `transform` on the positioned wrapper.
  const { isMounted, styles: transitionStyles } = useTransitionStyles(context, {
    duration: 120,
    initial: { opacity: 0, transform: "scale(0.96)" },
  });

  const menuContext = useMemo<MenuContextValue>(
    () => ({
      activeIndex,
      getItemProps,
      close: () => setIsOpen(false),
    }),
    [activeIndex, getItemProps]
  );

  return (
    <>
      <button
        type="button"
        ref={refs.setReference}
        aria-label={triggerLabel}
        className={cn("btn btn-ghost btn-circle", triggerClassName)}
        {...getReferenceProps()}
      >
        {trigger}
      </button>

      {isMounted ? (
        <FloatingPortal root={portalRoot ?? undefined}>
          <FloatingFocusManager context={context} modal={false}>
            <div
              ref={refs.setFloating}
              style={{
                ...floatingStyles,
                zIndex: Z_LAYERS.dropdown,
                visibility: isReferenceHidden ? "hidden" : "visible",
              }}
              {...getFloatingProps()}
            >
              {/* role="none" on the list wrappers keeps the menuitems direct
                  owned children of role="menu" — an <li>'s implicit listitem
                  role would otherwise break that relationship. */}
              <ul
                role="none"
                style={transitionStyles}
                className={cn(
                  "menu p-2 shadow bg-base-100 rounded-box overflow-y-auto",
                  className
                )}
              >
                <FloatingList elementsRef={elementsRef} labelsRef={labelsRef}>
                  <MenuContext.Provider value={menuContext}>
                    {children}
                  </MenuContext.Provider>
                </FloatingList>
              </ul>
            </div>
          </FloatingFocusManager>
        </FloatingPortal>
      ) : null}
    </>
  );
};

export type MenuItemProps = {
  onClick?: () => void;
  disabled?: boolean;
  /** Why the action is unavailable; surfaced as the item's tooltip. */
  disabledReason?: string;
  icon?: LucideIcon;
  iconSize?: number;
  className?: string;
  children: ReactNode;
};

/**
 * A row in a `Menu`.
 *
 * `disabled` is expressed with `aria-disabled` plus a guard on the handler
 * rather than the `disabled` attribute: a disabled button suppresses hover in
 * some browsers, which would swallow the `title` explaining *why* the action is
 * unavailable. The guard is what actually stops the action; the class only
 * greys it out.
 */
export const MenuItem = ({
  onClick,
  disabled = false,
  disabledReason,
  icon: Icon,
  iconSize = 16,
  className,
  children,
}: MenuItemProps) => {
  const menu = useMenuContext();
  const { ref, index } = useListItem();
  const isActive = menu.activeIndex === index;

  return (
    <li role="none">
      <button
        type="button"
        ref={ref}
        role="menuitem"
        tabIndex={isActive ? 0 : -1}
        aria-disabled={disabled || undefined}
        title={disabled ? disabledReason : undefined}
        className={cn(disabled && "disabled opacity-50", className)}
        {...menu.getItemProps({
          onClick: () => {
            if (disabled) return;
            onClick?.();
            menu.close();
          },
        })}
      >
        {Icon ? <Icon size={iconSize} /> : null}
        {children}
      </button>
    </li>
  );
};

export default Menu;
