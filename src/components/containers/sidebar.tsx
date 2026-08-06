import { cn, ucfirst } from "@/lib/utils";
import {
  ArchiveIcon,
  HardDrive,
  KeySquare,
  LayoutDashboard,
  LogOut,
  Palette,
} from "lucide-react";
import { Menu as NavMenu } from "react-daisyui";
import { Link, useLocation } from "react-router-dom";
import Button from "../ui/button";
import Menu, { MenuItem } from "../ui/menu";
import { themes } from "@/app/themes";
import appStore from "@/stores/app-store";
import garageLogo from "@/assets/garage-logo.svg";
import { useMutation } from "@tanstack/react-query";
import api from "@/lib/api";
import * as utils from "@/lib/utils";
import { toast } from "sonner";
import { useAuth } from "@/hooks/useAuth";

const pages = [
  { icon: LayoutDashboard, title: "Dashboard", path: "/", exact: true },
  { icon: HardDrive, title: "Cluster", path: "/cluster" },
  { icon: ArchiveIcon, title: "Buckets", path: "/buckets" },
  { icon: KeySquare, title: "Keys", path: "/keys" },
];

const Sidebar = () => {
  const { pathname } = useLocation();
  const auth = useAuth();

  return (
    <aside className="bg-base-100 border-r border-base-300/30 w-[80%] md:w-[250px] flex flex-col items-stretch overflow-hidden h-full">
      <div className="p-4">
        <img
          src={garageLogo}
          alt="logo"
          className="w-full max-w-[100px] mx-auto"
        />
        <p className="text-sm font-medium text-center">WebUI-NG</p>
      </div>

      <NavMenu className="gap-y-1 flex-1 overflow-y-auto">
        {pages.map((page) => {
          const isActive = page.exact
            ? pathname === page.path
            : pathname.startsWith(page.path);
          return (
            <NavMenu.Item key={page.path}>
              <Link
                to={page.path}
                className={cn(
                  "h-12 flex items-center px-6",
                  isActive &&
                    "bg-primary text-primary-content hover:bg-primary/60 focus:bg-primary focus:text-primary-content"
                )}
              >
                <page.icon size={18} />
                <p>{page.title}</p>
              </Link>
            </NavMenu.Item>
          );
        })}
      </NavMenu>

      <div className="py-2 px-4 flex items-center gap-2">
        <Menu
          placement="top-start"
          triggerLabel="Theme"
          triggerClassName={cn("btn btn-ghost", auth.isEnabled && "btn-circle")}
          trigger={
            <>
              <Palette size={18} className={!auth.isEnabled ? "-ml-1" : ""} />
              {!auth.isEnabled ? "Theme" : null}
            </>
          }
          className="max-h-[500px] overflow-y-auto"
        >
          {themes.map((theme) => (
            <MenuItem key={theme} onClick={() => appStore.setTheme(theme)}>
              {ucfirst(theme)}
            </MenuItem>
          ))}
        </Menu>

        {auth.isEnabled ? <LogoutButton /> : null}
      </div>
    </aside>
  );
};

const LogoutButton = () => {
  const logout = useMutation({
    mutationFn: () => api.post("/auth/logout"),
    onSuccess: () => {
      window.location.href = utils.url("/auth/login");
    },
    onError: (err) => {
      toast.error(err?.message || "Unknown error");
    },
  });

  return (
    <Button className="flex-1" icon={LogOut} onClick={() => logout.mutate()}>
      Logout
    </Button>
  );
};

export default Sidebar;
