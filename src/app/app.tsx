import { PageContextProvider } from "@/context/page-context";
import Router from "./router";
import { Toaster } from "sonner";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import ThemeProvider from "@/components/containers/theme-provider";
import ErrorBoundary from "@/components/containers/error-boundary";
import "./styles.css";

const App = () => {
  const [queryClient] = useState(() => new QueryClient());

  return (
    <PageContextProvider>
      <QueryClientProvider client={queryClient}>
        <ErrorBoundary>
          <Router />
        </ErrorBoundary>
      </QueryClientProvider>
      <Toaster richColors />
      <ThemeProvider />
    </PageContextProvider>
  );
};

export default App;
