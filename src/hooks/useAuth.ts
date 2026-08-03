import api from "@/lib/api";
import { useQuery } from "@tanstack/react-query";

type AuthResponse = {
  enabled: boolean;
  authenticated: boolean;
  username?: string;
  role?: string;
  // True when the deployment has no users yet, so the first administrator
  // still has to be created.
  needsSetup: boolean;
};

export const useAuth = () => {
  const { data, isLoading } = useQuery({
    queryKey: ["auth"],
    queryFn: () => api.get<AuthResponse>("/auth/status"),
    retry: false,
  });
  const role = data?.role;
  return {
    isLoading,
    // Authentication is mandatory; the server always reports true. Kept as a
    // field so the UI does not have to change if that ever gains nuance.
    isEnabled: data?.enabled ?? true,
    isAuthenticated: data?.authenticated ?? false,
    needsSetup: data?.needsSetup ?? false,
    username: data?.username,
    role,
    // Everyone except a viewer may write. The server is authoritative — this
    // only decides what the UI bothers to render.
    canWrite: role !== "viewer",
  };
};
