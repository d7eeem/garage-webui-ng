import { useMutation, useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { setupSchema } from "./schema";
import api from "@/lib/api";
import { toast } from "sonner";

// Creating the first administrator also signs it in, so invalidating the auth
// query is what moves the UI from the wizard into the app.
export const useSetup = () => {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (body: z.infer<typeof setupSchema>) => {
      return api.post("/setup", { body });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["auth"] });
    },
    onError: (err) => {
      toast.error(err?.message || "Unknown error");
    },
  });
};
