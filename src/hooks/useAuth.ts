import api from "@/lib/api";
import { useQuery } from "@tanstack/react-query";

type AuthResponse = {
  enabled: boolean;
  authenticated: boolean;
  username?: string;
  role?: string;
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
    isEnabled: data?.enabled ?? false,
    isAuthenticated: data?.authenticated ?? false,
    username: data?.username,
    role,
    // auth off ⇒ everyone is effectively admin (matches the middleware
    // pass-through); auth on ⇒ only non-viewers can write.
    canWrite: !(data?.enabled) || role !== "viewer",
  };
};
