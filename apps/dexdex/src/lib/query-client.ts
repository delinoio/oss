import { QueryClient } from "@tanstack/react-query";

export const dexdexQueryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});
