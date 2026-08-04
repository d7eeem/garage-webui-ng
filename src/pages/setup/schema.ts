import { z } from "zod";

// The minimum length mirrors store.MinPasswordLength on the server. The server
// validates independently — this only spares the user a round trip.
export const setupSchema = z
  .object({
    username: z.string().min(1, "Username is required"),
    password: z.string().min(10, "Password must be at least 10 characters"),
    confirmPassword: z.string().min(1, "Please confirm your password"),
  })
  .refine((v) => v.password === v.confirmPassword, {
    message: "Passwords do not match",
    path: ["confirmPassword"],
  });
