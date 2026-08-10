import { Modal } from "react-daisyui";
import Button from "@/components/ui/button";

type Props = {
  bucketName: string;
  isOpen: boolean;
  onCancel: () => void;
  onConfirm: () => void;
};

/**
 * Guards only the enable direction of the website-access toggle — see
 * overview-website-access.tsx. Purely presentational: no data fetching, no
 * mutation inside it, so it stays trivially testable in isolation.
 */
const PublicAccessConfirm = ({
  bucketName,
  isOpen,
  onCancel,
  onConfirm,
}: Props) => {
  return (
    <Modal open={isOpen}>
      <Modal.Header>Enable public access?</Modal.Header>
      <Modal.Body>
        <p>
          Every object in <span className="font-medium">{bucketName}</span>{" "}
          becomes readable by anyone who can reach the Garage website
          endpoint, with no sign-in. Uploads and deletions still require
          credentials. You can turn this off again at any time.
        </p>
      </Modal.Body>
      <Modal.Actions>
        <Button color="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button color="warning" onClick={onConfirm}>
          Enable public access
        </Button>
      </Modal.Actions>
    </Modal>
  );
};

export default PublicAccessConfirm;
