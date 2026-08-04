import Button from "@/components/ui/button";
import { InputField } from "@/components/ui/input";
import { useAuth } from "@/hooks/useAuth";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { Card } from "react-daisyui";
import { changePasswordSchema, ChangePasswordSchema } from "./schema";
import { useChangePassword } from "./hooks";

const emptyForm: ChangePasswordSchema = {
  currentPassword: "",
  newPassword: "",
  confirmPassword: "",
};

/**
 * The signed-in user's own account. Available to every role — a read-only
 * viewer owns its credential too, and the server allows exactly this one write
 * for them (middleware.isViewerAllowed).
 */
const AccountTab = () => {
  const auth = useAuth();

  const form = useForm<ChangePasswordSchema>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: emptyForm,
  });

  const changePassword = useChangePassword({
    // Never leave a typed password sitting in the DOM after a success.
    onSuccess: () => form.reset(emptyForm),
  });

  return (
    <Card className="bg-base-100 max-w-xl" bordered>
      <Card.Body>
        <Card.Title tag="h2">Change password</Card.Title>

        {auth.username ? (
          <p className="text-sm text-base-content/60">
            Signed in as <span className="font-medium">{auth.username}</span>
          </p>
        ) : null}

        <form
          onSubmit={form.handleSubmit((values) =>
            changePassword.mutate(values)
          )}
        >
          {/* Helps password managers associate the update with the right
              account; it is not submitted. */}
          <input
            type="text"
            name="username"
            autoComplete="username"
            value={auth.username ?? ""}
            readOnly
            hidden
          />

          <InputField
            form={form}
            name="currentPassword"
            title="Current password"
            type="password"
            autoComplete="current-password"
          />

          <InputField
            form={form}
            name="newPassword"
            title="New password"
            type="password"
            placeholder="At least 10 characters"
            autoComplete="new-password"
          />

          <InputField
            form={form}
            name="confirmPassword"
            title="Confirm new password"
            type="password"
            placeholder="Repeat the new password"
            autoComplete="new-password"
          />

          <Card.Actions className="mt-4">
            <Button
              type="submit"
              color="primary"
              className="w-full md:w-auto min-w-[100px]"
              loading={changePassword.isPending}
            >
              Save
            </Button>
          </Card.Actions>
        </form>
      </Card.Body>
    </Card>
  );
};

export default AccountTab;
