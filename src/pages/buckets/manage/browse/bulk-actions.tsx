import { Dispatch, SetStateAction, useState } from "react";
import { Alert, Modal } from "react-daisyui";
import { CircleAlertIcon, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";

import Button from "@/components/ui/button";
import { useDisclosure } from "@/hooks/useDisclosure";
import { handleError } from "@/lib/utils";
import { useBulkDelete } from "./hooks";
import { BulkDeleteResult } from "./types";

type Props = {
  bucketName: string;
  selected: Set<string>;
  setSelected: Dispatch<SetStateAction<Set<string>>>;
};

const PREVIEW_COUNT = 5;

const BulkActions = ({ bucketName, selected, setSelected }: Props) => {
  const { isOpen, onOpen, onClose } = useDisclosure();
  const queryClient = useQueryClient();
  const [result, setResult] = useState<BulkDeleteResult | null>(null);

  const selectedKeys = Array.from(selected);

  const bulkDelete = useBulkDelete(bucketName, {
    onSuccess: (data, keys) => {
      const failedKeys = new Set(data.errors.map((e) => e.key));
      const succeededKeys = keys.filter((key) => !failedKeys.has(key));

      // Only drop the keys that are actually gone — a failed key stays
      // selected so the user can see it in context and retry.
      setSelected((prev) => {
        const next = new Set(prev);
        succeededKeys.forEach((key) => next.delete(key));
        return next;
      });

      queryClient.invalidateQueries({ queryKey: ["browse", bucketName] });

      if (data.errors.length === 0) {
        toast.success(
          `Deleted ${data.deleted} object${data.deleted === 1 ? "" : "s"}`
        );
        setResult(null);
        onClose();
      } else {
        setResult(data);
      }
    },
    onError: handleError,
  });

  const closeModal = () => {
    setResult(null);
    onClose();
  };

  const onConfirmDelete = () => {
    bulkDelete.mutate(selectedKeys);
  };

  const total = result ? result.deleted + result.errors.length : 0;

  return (
    <div className="flex flex-row items-center justify-between gap-2 mx-2 mb-2 px-3 py-2 bg-base-200 rounded-lg">
      <span className="text-sm font-medium">
        {selected.size} object{selected.size === 1 ? "" : "s"} selected
      </span>

      <Button
        icon={Trash2}
        color="error"
        size="sm"
        onClick={onOpen}
      >
        Delete selected
      </Button>

      <Modal open={isOpen}>
        <Modal.Header>
          {result ? "Delete results" : "Delete selected objects"}
        </Modal.Header>

        <Modal.Body>
          {result ? (
            <div className="flex flex-col gap-2">
              {/* result is only ever set when there's at least one failure
                  (see onSuccess above) — the all-success path toasts and
                  closes the modal instead, so this is always "warning". */}
              <Alert status="warning" icon={<CircleAlertIcon />}>
                <span>
                  Deleted {result.deleted} of {total} — {result.errors.length}{" "}
                  failed
                </span>
              </Alert>

              <ul className="text-sm max-h-48 overflow-y-auto list-disc pl-5">
                {result.errors.map((e) => (
                  <li key={e.key}>
                    <span className="font-mono">{e.key}</span>: {e.message}
                  </li>
                ))}
              </ul>
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              <p>
                Are you sure you want to delete {selectedKeys.length} selected
                object{selectedKeys.length === 1 ? "" : "s"}? This action
                cannot be undone.
              </p>

              <ul className="text-sm max-h-48 overflow-y-auto list-disc pl-5 font-mono">
                {selectedKeys.slice(0, PREVIEW_COUNT).map((key) => (
                  <li key={key}>{key}</li>
                ))}
                {selectedKeys.length > PREVIEW_COUNT && (
                  <li className="font-sans">
                    …and {selectedKeys.length - PREVIEW_COUNT} more
                  </li>
                )}
              </ul>
            </div>
          )}
        </Modal.Body>

        <Modal.Actions>
          {result ? (
            <Button onClick={closeModal}>Close</Button>
          ) : (
            <>
              <Button onClick={onClose} disabled={bulkDelete.isPending}>
                Cancel
              </Button>
              <Button
                color="error"
                onClick={onConfirmDelete}
                disabled={bulkDelete.isPending}
              >
                {bulkDelete.isPending
                  ? "Deleting…"
                  : `Delete ${selectedKeys.length}`}
              </Button>
            </>
          )}
        </Modal.Actions>
      </Modal>
    </div>
  );
};

export default BulkActions;
