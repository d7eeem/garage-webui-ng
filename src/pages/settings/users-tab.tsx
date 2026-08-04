import Button from "@/components/ui/button";
import { InputField } from "@/components/ui/input";
import { useAuth } from "@/hooks/useAuth";
import { useDisclosure } from "@/hooks/useDisclosure";
import { cn, dayjs, handleError } from "@/lib/utils";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  EllipsisVertical,
  KeyRound,
  Plus,
  ShieldCheck,
  ShieldOff,
  Trash,
  UserCheck,
  UserX,
} from "lucide-react";
import { ReactNode, useEffect, useMemo } from "react";
import { Badge, Card, Dropdown, Modal, Table } from "react-daisyui";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import {
  useCreateUser,
  useDeleteUser,
  useResetPassword,
  useUpdateUser,
  useUsers,
} from "./users-hooks";
import {
  createUserSchema,
  CreateUserSchema,
  resetPasswordSchema,
  ResetPasswordSchema,
  User,
} from "./users-schema";

const emptyCreateForm: CreateUserSchema = {
  username: "",
  password: "",
  role: "viewer",
};

/** Formats a nullable timestamp; the em dash marks "never". */
const formatDate = (value?: string | null) =>
  value ? dayjs(value).format("YYYY-MM-DD HH:mm") : "—";

type ActionItemProps = {
  /** Why the action is unavailable. Undefined means it is allowed. */
  blockedBecause?: string;
  className?: string;
  onClick: () => void;
  children: ReactNode;
};

/**
 * A row in the per-user action menu that can be greyed out.
 *
 * react-daisyui's `Dropdown.Item` renders an anchor and has no `disabled`
 * prop, so the state is expressed with daisyUI's `disabled` menu class *and* a
 * guard on the handler — styling alone would still leave the row clickable by
 * keyboard. The reason becomes the tooltip, so a greyed-out Delete explains
 * itself instead of looking broken.
 */
const ActionItem = ({
  blockedBecause,
  className,
  onClick,
  children,
}: ActionItemProps) => (
  <Dropdown.Item
    className={cn(blockedBecause && "disabled opacity-50", className)}
    title={blockedBecause}
    aria-disabled={blockedBecause ? true : undefined}
    onClick={(e) => {
      e.preventDefault();
      if (blockedBecause) return;
      onClick();
    }}
  >
    {children}
  </Dropdown.Item>
);

/**
 * Settings → Users: create, rename, re-role, disable and delete accounts.
 *
 * The client-side `disabled` states below are a courtesy, not a control. The
 * server enforces the same rules — an admin may not delete, demote or disable
 * themselves, and the last enabled administrator may not be removed at all —
 * and answers 409 with a message this component surfaces verbatim. Never
 * relax a server guard because the UI already hides the button.
 */
const UsersTab = () => {
  const auth = useAuth();
  const isAdmin = auth.role === "admin";

  const { data: users, isLoading } = useUsers(isAdmin);

  const createDialog = useDisclosure();
  const resetDialog = useDisclosure<User>();

  const createForm = useForm<CreateUserSchema>({
    resolver: zodResolver(createUserSchema),
    defaultValues: emptyCreateForm,
  });

  const resetForm = useForm<ResetPasswordSchema>({
    resolver: zodResolver(resetPasswordSchema),
    defaultValues: { newPassword: "" },
  });

  // setFocus is a stable reference, so depending on it (rather than on the
  // whole form object) keeps these effects firing only when a dialog opens.
  const { setFocus: setCreateFocus } = createForm;
  const { setFocus: setResetFocus } = resetForm;

  useEffect(() => {
    if (createDialog.isOpen) setCreateFocus("username");
  }, [createDialog.isOpen, setCreateFocus]);

  useEffect(() => {
    if (resetDialog.isOpen) setResetFocus("newPassword");
  }, [resetDialog.isOpen, setResetFocus]);

  const createUser = useCreateUser({
    onSuccess: (user) => {
      createDialog.onClose();
      // Never leave a typed password sitting in the DOM after a success.
      createForm.reset(emptyCreateForm);
      toast.success(`User ${user.username} created`);
    },
    onError: handleError,
  });

  const updateUser = useUpdateUser({
    onSuccess: (user) => toast.success(`User ${user.username} updated`),
    onError: handleError,
  });

  const deleteUser = useDeleteUser({
    onSuccess: () => toast.success("User deleted"),
    onError: handleError,
  });

  const resetPassword = useResetPassword({
    onSuccess: () => {
      resetDialog.onClose();
      resetForm.reset({ newPassword: "" });
      toast.success("Password reset");
    },
    onError: handleError,
  });

  // How many accounts could still administer this instance. When it reaches
  // one, that account's destructive actions are greyed out — the server would
  // refuse them anyway.
  const enabledAdmins = useMemo(
    () => (users ?? []).filter((u) => u.role === "admin" && !u.disabled).length,
    [users]
  );

  const isSelf = (user: User) =>
    !!auth.username &&
    auth.username.toLowerCase() === user.username.toLowerCase();

  const isLastAdmin = (user: User) =>
    user.role === "admin" && !user.disabled && enabledAdmins <= 1;

  /**
   * Why an action that would take administration away is unavailable, or
   * undefined when it is allowed. These mirror `ensureNotLastAdmin` on the
   * server, which is the actual enforcement point — this exists so the reason
   * is visible before the click, not to replace the check.
   */
  const blockReason = (user: User, action: "delete" | "demote" | "disable") => {
    if (isSelf(user)) {
      return action === "delete"
        ? "You cannot delete your own account"
        : "You cannot demote or disable your own account";
    }
    if (isLastAdmin(user)) {
      return "This is the last enabled administrator";
    }
    return undefined;
  };

  const onDelete = (user: User) => {
    if (
      window.confirm(
        `Delete the account "${user.username}"? This cannot be undone.`
      )
    ) {
      deleteUser.mutate(user.id);
    }
  };

  const onSubmitCreate = createForm.handleSubmit((values) =>
    createUser.mutate(values)
  );

  const onSubmitReset = resetForm.handleSubmit((values) => {
    const user = resetDialog.data;
    if (!user) return;
    resetPassword.mutate({ id: user.id, newPassword: values.newPassword });
  });

  if (auth.isLoading) {
    // The role is not known yet; showing "access required" here would flash a
    // misleading message at an administrator on every page load.
    return null;
  }

  if (!isAdmin) {
    return (
      <Card className="bg-base-100 max-w-xl" bordered>
        <Card.Body>
          <Card.Title tag="h2">Administrator access required</Card.Title>
          <p className="text-sm text-base-content/60">
            Only administrators can manage user accounts. Your own password can
            be changed from the Account tab.
          </p>
        </Card.Body>
      </Card>
    );
  }

  return (
    <>
      <div className="flex flex-row items-center gap-2">
        <div className="flex-1">
          <h2 className="text-lg font-medium">Users</h2>
          <p className="text-sm text-base-content/60">
            Administrators manage the cluster; viewers have read-only access.
          </p>
        </div>

        <Button icon={Plus} color="primary" onClick={() => createDialog.onOpen()}>
          Create User
        </Button>
      </div>

      <Card className="card-body mt-4 p-4">
        <div className="w-full overflow-x-auto">
          <Table zebra>
            <Table.Head>
              <span>Username</span>
              <span>Role</span>
              <span>Status</span>
              <span>Last login</span>
              <span>Created</span>
              <span />
            </Table.Head>

            <Table.Body>
              {(users ?? []).map((user) => (
                <Table.Row key={user.id}>
                  <span className="font-medium">
                    {user.username}
                    {isSelf(user) ? (
                      <span className="ml-2 text-xs text-base-content/50">
                        (you)
                      </span>
                    ) : null}
                  </span>

                  <span>
                    <Badge color={user.role === "admin" ? "primary" : "ghost"}>
                      {user.role}
                    </Badge>
                  </span>

                  <span>
                    <Badge color={user.disabled ? "warning" : "success"}>
                      {user.disabled ? "Disabled" : "Active"}
                    </Badge>
                  </span>

                  <span className="whitespace-nowrap">
                    {formatDate(user.lastLogin)}
                  </span>
                  <span className="whitespace-nowrap">
                    {formatDate(user.createdAt)}
                  </span>

                  <span>
                    <Dropdown end>
                      <Dropdown.Toggle button={false}>
                        <Button icon={EllipsisVertical} color="ghost" />
                      </Dropdown.Toggle>

                      <Dropdown.Menu className="z-10 w-56">
                        {/* Resetting a password takes nobody's administration
                            away, so it is never blocked. */}
                        <ActionItem onClick={() => resetDialog.onOpen(user)}>
                          <KeyRound size={16} /> Reset password
                        </ActionItem>

                        <ActionItem
                          // Promoting a viewer only ever adds an administrator;
                          // only the demotion needs guarding.
                          blockedBecause={
                            user.role === "admin"
                              ? blockReason(user, "demote")
                              : undefined
                          }
                          onClick={() =>
                            updateUser.mutate({
                              id: user.id,
                              role: user.role === "admin" ? "viewer" : "admin",
                            })
                          }
                        >
                          {user.role === "admin" ? (
                            <>
                              <ShieldOff size={16} /> Make viewer
                            </>
                          ) : (
                            <>
                              <ShieldCheck size={16} /> Make administrator
                            </>
                          )}
                        </ActionItem>

                        <ActionItem
                          // Re-enabling is always safe; only disabling is not.
                          blockedBecause={
                            user.disabled
                              ? undefined
                              : blockReason(user, "disable")
                          }
                          onClick={() =>
                            updateUser.mutate({
                              id: user.id,
                              disabled: !user.disabled,
                            })
                          }
                        >
                          {user.disabled ? (
                            <>
                              <UserCheck size={16} /> Enable
                            </>
                          ) : (
                            <>
                              <UserX size={16} /> Disable
                            </>
                          )}
                        </ActionItem>

                        <ActionItem
                          className="bg-error/10 text-error"
                          blockedBecause={blockReason(user, "delete")}
                          onClick={() => onDelete(user)}
                        >
                          <Trash size={16} /> Delete
                        </ActionItem>
                      </Dropdown.Menu>
                    </Dropdown>
                  </span>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>

          {!isLoading && !users?.length ? (
            <p className="py-6 text-center text-base-content/60">
              No users found.
            </p>
          ) : null}
        </div>
      </Card>

      {/* Create user */}
      <Modal ref={createDialog.dialogRef} backdrop open={createDialog.isOpen}>
        <Modal.Header className="mb-1">Create User</Modal.Header>
        <Modal.Body>
          <form onSubmit={onSubmitCreate}>
            <InputField
              form={createForm}
              name="username"
              title="Username"
              autoComplete="off"
            />

            <InputField
              form={createForm}
              name="password"
              title="Password"
              type="password"
              placeholder="At least 10 characters"
              autoComplete="new-password"
            />

            <div className="form-control">
              <label className="label label-text" htmlFor="new-user-role">
                Role
              </label>
              <select
                id="new-user-role"
                className="select select-bordered"
                {...createForm.register("role")}
              >
                <option value="viewer">Viewer (read-only)</option>
                <option value="admin">Administrator</option>
              </select>
            </div>
          </form>
        </Modal.Body>

        <Modal.Actions>
          <Button
            onClick={() => {
              createDialog.onClose();
              createForm.reset(emptyCreateForm);
            }}
          >
            Cancel
          </Button>
          <Button
            color="primary"
            disabled={createUser.isPending}
            onClick={onSubmitCreate}
          >
            Create
          </Button>
        </Modal.Actions>
      </Modal>

      {/* Reset password */}
      <Modal ref={resetDialog.dialogRef} backdrop open={resetDialog.isOpen}>
        <Modal.Header className="mb-1">
          Reset password{resetDialog.data ? ` — ${resetDialog.data.username}` : ""}
        </Modal.Header>
        <Modal.Body>
          <p className="text-sm text-base-content/60">
            The new password is not shown again after this dialog closes. Share
            it with the user over a channel you trust.
          </p>

          <form onSubmit={onSubmitReset}>
            <InputField
              form={resetForm}
              name="newPassword"
              title="New password"
              type="password"
              placeholder="At least 10 characters"
              autoComplete="new-password"
            />
          </form>
        </Modal.Body>

        <Modal.Actions>
          <Button
            onClick={() => {
              resetDialog.onClose();
              resetForm.reset({ newPassword: "" });
            }}
          >
            Cancel
          </Button>
          <Button
            color="primary"
            disabled={resetPassword.isPending}
            onClick={onSubmitReset}
          >
            Reset
          </Button>
        </Modal.Actions>
      </Modal>
    </>
  );
};

export default UsersTab;
