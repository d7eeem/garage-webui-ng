import { useAuth } from "@/hooks/useAuth";
import { Navigate, Outlet } from "react-router-dom";

const AuthLayout = () => {
  const auth = useAuth();

  if (auth.isLoading) {
    return null;
  }

  // A fresh instance has no account to sign in with, so /auth/login bounces to
  // the first-run wizard.
  if (auth.needsSetup) {
    return <Navigate to="/setup" replace />;
  }

  if (auth.isAuthenticated) {
    return <Navigate to="/" replace />;
  }

  return (
    <div className="min-h-svh flex items-center justify-center">
      <Outlet />
    </div>
  );
};

export default AuthLayout;
