import { Check } from "lucide-react";

type SaveStatusProps = {
  isPending: boolean;
  isSuccess: boolean;
  isError: boolean;
};

/**
 * Persistent marker for an auto-save mutation with no Save button.
 * `useUpdateBucket`'s default `onError` already toasts the reason for a
 * failure — this is the lasting indicator that the form is (or isn't) in
 * sync with the server, since a toast disappears and a keystroke doesn't.
 *
 * Does not auto-clear on a timer: TanStack Query resets `isSuccess`/`isError`
 * on the next mutation, which is the behaviour we want.
 */
const SaveStatus = ({ isPending, isSuccess, isError }: SaveStatusProps) => {
  if (isPending) {
    return <span className="text-xs text-base-content/60">Saving…</span>;
  }
  if (isSuccess) {
    return (
      <span className="text-xs text-success flex items-center gap-1">
        <Check size={12} /> Saved
      </span>
    );
  }
  if (isError) {
    return <span className="text-xs text-error">Not saved</span>;
  }
  return null;
};

export default SaveStatus;
