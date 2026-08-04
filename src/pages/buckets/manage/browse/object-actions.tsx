import { Object } from "./types";
import Button from "@/components/ui/button";
import Menu, { MenuItem } from "@/components/ui/menu";
import { DownloadIcon, EllipsisVertical, Share2, Trash } from "lucide-react";
import { useDeleteObject } from "./hooks";
import { useBucketContext } from "../context";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { handleError } from "@/lib/utils";
import { API_URL } from "@/lib/api";
import { shareDialog } from "./share-dialog";
import { useAuth } from "@/hooks/useAuth";

type Props = {
  prefix?: string;
  object: Pick<Object, "objectKey" | "url">;
};

const ObjectActions = ({ prefix = "", object }: Props) => {
  const { canWrite } = useAuth();
  const { bucketName } = useBucketContext();
  const queryClient = useQueryClient();
  const isDirectory = object.objectKey.endsWith("/");

  const deleteObject = useDeleteObject(bucketName, {
    onSuccess: () => {
      toast.success("Object deleted!");
      queryClient.invalidateQueries({ queryKey: ["browse", bucketName] });
    },
    onError: handleError,
  });

  const onDownload = () => {
    // object.url arrives percent-encoded from the API; do not re-encode.
    window.open(API_URL + object.url + "?dl=1", "_blank");
  };

  const onDelete = () => {
    if (
      window.confirm(
        `Are you sure you want to delete this ${
          isDirectory ? "directory and its content" : "object"
        }?`
      )
    ) {
      deleteObject.mutate({
        key: prefix + object.objectKey,
        recursive: isDirectory,
      });
    }
  };

  return (
    <td className="!p-0 w-auto">
      <span className="w-full flex flex-row justify-end pr-2">
        {!isDirectory && (
          <Button icon={DownloadIcon} color="ghost" onClick={onDownload} />
        )}

        <Menu
          trigger={<EllipsisVertical size={18} />}
          triggerLabel="Object actions"
          className="gap-y-1"
        >
          <MenuItem
            icon={Share2}
            iconSize={24}
            onClick={() => shareDialog.open({ key: object.objectKey, prefix })}
          >
            Share
          </MenuItem>
          {canWrite && (
            <MenuItem
              icon={Trash}
              iconSize={24}
              className="text-error bg-error/10"
              onClick={onDelete}
            >
              Delete
            </MenuItem>
          )}
        </Menu>
      </span>
    </td>
  );
};

export default ObjectActions;
