import Menu, { MenuItem } from "@/components/ui/menu";
import { EllipsisVertical, Trash } from "lucide-react";
import { useNavigate, useParams } from "react-router-dom";
import { useRemoveBucket } from "../hooks";
import { toast } from "sonner";
import { handleError } from "@/lib/utils";
import { useAuth } from "@/hooks/useAuth";

const MenuButton = () => {
  const { canWrite } = useAuth();
  const { id } = useParams();
  const navigate = useNavigate();

  const removeBucket = useRemoveBucket({
    onSuccess: () => {
      toast.success("Bucket removed!");
      navigate("/buckets", { replace: true });
    },
    onError: handleError,
  });

  const onRemove = () => {
    if (window.confirm("Are you sure you want to remove this bucket?")) {
      removeBucket.mutate(id!);
    }
  };

  if (!canWrite) {
    // Only mutating actions live in this menu; nothing read-only to keep.
    return null;
  }

  return (
    <Menu
      trigger={<EllipsisVertical size={18} />}
      triggerLabel="Bucket actions"
      placement="bottom-end"
    >
      <MenuItem
        icon={Trash}
        iconSize={24}
        onClick={onRemove}
        className="bg-error/10 text-error"
      >
        Remove
      </MenuItem>
    </Menu>
  );
};

export default MenuButton;
