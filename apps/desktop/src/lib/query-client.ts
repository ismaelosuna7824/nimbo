import { QueryClient } from "@tanstack/react-query"

/** The app-wide query client, shared with imperative actions outside React. */
export const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false } },
})
