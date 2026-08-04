import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { lazy, Suspense } from "react";
import AuthLayout from "@/components/layouts/auth-layout";
import MainLayout from "@/components/layouts/main-layout";
import { BASE_PATH } from "@/lib/consts";

const LoginPage = lazy(() => import("@/pages/auth/login"));
const SetupPage = lazy(() => import("@/pages/setup/page"));
const ClusterPage = lazy(() => import("@/pages/cluster/page"));
const HomePage = lazy(() => import("@/pages/home/page"));
const BucketsPage = lazy(() => import("@/pages/buckets/page"));
const ManageBucketPage = lazy(() => import("@/pages/buckets/manage/page"));
const KeysPage = lazy(() => import("@/pages/keys/page"));
const SettingsPage = lazy(() => import("@/pages/settings/page"));

const router = createBrowserRouter(
  [
    {
      // The first-run wizard sits outside both layouts: each of them redirects
      // here while the instance has no users, so nesting it under one would
      // loop. SetupPage brings its own centred shell and its own guard.
      path: "/setup",
      Component: SetupPage,
    },
    {
      path: "/auth",
      Component: AuthLayout,
      children: [
        {
          path: "login",
          Component: LoginPage,
        },
      ],
    },
    {
      path: "/",
      Component: MainLayout,
      children: [
        {
          index: true,
          Component: HomePage,
        },
        {
          path: "cluster",
          Component: ClusterPage,
        },
        {
          path: "buckets",
          children: [
            { index: true, Component: BucketsPage },
            { path: ":id", Component: ManageBucketPage },
          ],
        },
        {
          path: "keys",
          Component: KeysPage,
        },
        {
          path: "settings",
          Component: SettingsPage,
        },
      ],
    },
  ],
  {
    basename: BASE_PATH,
  }
);

const Router = () => {
  return (
    <Suspense>
      <RouterProvider router={router} />
    </Suspense>
  );
};

export default Router;
