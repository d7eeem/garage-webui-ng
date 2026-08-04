import Button from "@/components/ui/button";
import { zodResolver } from "@hookform/resolvers/zod";
import { Card } from "react-daisyui";
import { useForm } from "react-hook-form";
import { Navigate } from "react-router-dom";
import { setupSchema } from "./schema";
import { InputField } from "@/components/ui/input";
import { useSetup } from "./hooks";
import { useAuth } from "@/hooks/useAuth";

// The first-run wizard. It is a top-level route rather than a child of
// AuthLayout on purpose: AuthLayout redirects to /setup while needsSetup is
// true, so nesting the wizard inside it would loop. The centring shell is
// therefore repeated here.
//
// Creating the account also signs the caller in, so needsSetup flips to false
// and the guard below carries them into the app.
export default function SetupPage() {
  const auth = useAuth();
  const form = useForm({
    resolver: zodResolver(setupSchema),
    defaultValues: { username: "", password: "", confirmPassword: "" },
  });
  const setup = useSetup();

  if (auth.isLoading) {
    return null;
  }

  // An instance that already has an account must never be able to re-open the
  // wizard; the server refuses it too (409).
  if (!auth.needsSetup) {
    return <Navigate to="/" replace />;
  }

  return (
    <div className="min-h-svh flex items-center justify-center p-4">
      <form onSubmit={form.handleSubmit((v) => setup.mutate(v))}>
        <Card className="w-full max-w-md" bordered>
          <Card.Body>
            <Card.Title tag="h2">Welcome</Card.Title>
            <p className="text-base-content/60">
              Create the administrator account for this instance.
            </p>

            <InputField
              form={form}
              name="username"
              title="Username"
              placeholder="Choose a username"
              autoComplete="username"
            />

            <InputField
              form={form}
              name="password"
              title="Password"
              type="password"
              placeholder="At least 10 characters"
              autoComplete="new-password"
            />

            <InputField
              form={form}
              name="confirmPassword"
              title="Confirm password"
              type="password"
              placeholder="Repeat the password"
              autoComplete="new-password"
            />

            <Card.Actions className="mt-4">
              <Button
                type="submit"
                color="primary"
                className="w-full md:w-auto min-w-[100px]"
                loading={setup.isPending}
              >
                Create administrator
              </Button>
            </Card.Actions>

            <p className="text-sm text-base-content/60 mt-2">
              This is a one-time setup. You can add more users later from
              Settings.
            </p>
          </Card.Body>
        </Card>
      </form>
    </div>
  );
}
