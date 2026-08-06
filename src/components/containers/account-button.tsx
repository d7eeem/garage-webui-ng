import { useAuth } from "@/hooks/useAuth";
import { cn } from "@/lib/utils";
import { Settings } from "lucide-react";
import { Link, useLocation } from "react-router-dom";

/**
 * The account chip in the header's right corner: who you are signed in as, and
 * the way into Settings.
 *
 * It is a plain link rather than a dropdown because it has exactly one
 * destination — the theme picker and Logout deliberately stayed in the sidebar,
 * so a menu would cost a click to reach its only item.
 *
 * It must render unconditionally. `Settings` was removed from the sidebar nav
 * when this landed, which makes this chip the only route to that page; hiding
 * it behind any condition strands `/settings` at a URL nobody can click to.
 */
const AccountButton = () => {
  const { username } = useAuth();
  const { pathname } = useLocation();
  const isActive = pathname.startsWith("/settings");

  return (
    <Link
      to="/settings"
      className={cn(
        "btn btn-ghost gap-2 px-3 shrink-0 font-normal",
        isActive && "btn-active"
      )}
    >
      <Settings size={18} />
      {/* The visible label is the username, so the destination is only
          discoverable to a screen reader — and it disappears entirely at the
          `sm` breakpoint, leaving an unlabelled icon. This carries the name in
          both cases. */}
      <span className="sr-only">Settings</span>
      {username ? (
        <span className="hidden sm:inline max-w-[12rem] truncate">
          {username}
        </span>
      ) : null}
    </Link>
  );
};

export default AccountButton;
