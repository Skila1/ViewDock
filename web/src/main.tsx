import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import * as Tooltip from "@radix-ui/react-tooltip";
import { App } from "./App";
import "./index.css";
import { replayOfflineMutations } from "./api/client";
import { isDiscordActivity } from "./lib/discordActivity";

// Inside Discord's iframe the app is short-lived and served through Discord's
// proxy, so offline caching would only serve stale builds.
if (import.meta.env.PROD && "serviceWorker" in navigator && !isDiscordActivity()) {
  window.addEventListener("load", () => {
    void navigator.serviceWorker.register("/sw.js", { scope: "/" });
  });
}

if (typeof window !== "undefined") {
  window.addEventListener("online", () => {
    void replayOfflineMutations();
  });
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 15_000,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Tooltip.Provider delayDuration={250}>
          <App />
        </Tooltip.Provider>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
