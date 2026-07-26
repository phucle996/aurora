import { QueryClient } from "@tanstack/react-query";

// A principal switch must clear one shared cache instance; creating clients in
// component scope risks exposing completed queries from the previous IAM user.
export const queryClient = new QueryClient();
