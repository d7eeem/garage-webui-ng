import { z } from "zod";

/**
 * A user as the administration API returns it. There is deliberately no
 * password field of any kind: `store.User.PasswordHash` carries `json:"-"` on
 * the server, so a hash can never appear here, and nothing in the UI needs one.
 */
export type User = {
  id: number;
  username: string;
  role: Role;
  disabled: boolean;
  createdAt: string;
  updatedAt: string;
  lastLogin: string | null;
};

/** The two roles the server accepts (`store.RoleAdmin` / `store.RoleViewer`). */
export const roleSchema = z.enum(["admin", "viewer"]);
export type Role = z.infer<typeof roleSchema>;

// The username charset mirrors store.usernamePattern and the minimum password
// length mirrors store.MinPasswordLength. The server validates independently —
// these only spare the user a round trip.
export const createUserSchema = z.object({
  username: z
    .string()
    .min(1, "Username is required")
    .max(64, "Username must be at most 64 characters")
    .regex(
      /^[A-Za-z0-9._@-]+$/,
      "Only letters, digits and . _ @ - are allowed"
    ),
  password: z.string().min(10, "Password must be at least 10 characters"),
  role: roleSchema,
});

export type CreateUserSchema = z.infer<typeof createUserSchema>;

export const resetPasswordSchema = z.object({
  newPassword: z.string().min(10, "Password must be at least 10 characters"),
});

export type ResetPasswordSchema = z.infer<typeof resetPasswordSchema>;
