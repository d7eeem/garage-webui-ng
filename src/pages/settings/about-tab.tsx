import { useConfig } from "@/hooks/useConfig";
import { copyToClipboard, handleError } from "@/lib/utils";
import Button from "@/components/ui/button";
import Checkbox from "@/components/ui/checkbox";
import { Copy } from "lucide-react";
import { Card } from "react-daisyui";
import { useState } from "react";
import { ApplyUpdateResult, useApplyUpdate, useUpdateCheck } from "./hooks";

/**
 * Build identity of the running app, plus an opt-in check for a newer
 * release. The check itself is server-side and cached (backend/router/update.go)
 * — this component only renders whatever it returns; it never calls GitHub
 * directly.
 *
 * When the server reports canSelfUpdate (a signing key is configured and the
 * executable is writable — see backend/router/selfupdate.go), an "Update
 * now" button drives POST /update/apply: download, verify, stage. The
 * request defaults to `restart: false` — the binary is swapped on disk but
 * the running process keeps serving the old version until an operator
 * restarts it, so the update path can never lock an operator out of the
 * console. A separate, unchecked-by-default checkbox opts into the process
 * restarting itself after a successful swap.
 */
const AboutTab = () => {
  const { data: config } = useConfig();
  const { data: update } = useUpdateCheck();
  const [restartAfterInstall, setRestartAfterInstall] = useState(false);
  const [applyResult, setApplyResult] = useState<ApplyUpdateResult | null>(
    null
  );

  const applyUpdate = useApplyUpdate({
    onSuccess: (data) => {
      setApplyResult(data);
    },
    onError: handleError,
  });

  const onUpdateNow = () => {
    if (!update?.latest) return;

    const confirmed = window.confirm(
      `Install version ${update.latest}? A restart is required to apply it — the running process keeps serving the current version until then.`
    );
    if (!confirmed) return;

    applyUpdate.mutate({ restart: restartAfterInstall });
  };

  return (
    <Card className="bg-base-100 max-w-xl" bordered>
      <Card.Body>
        <Card.Title tag="h2">About</Card.Title>

        <div className="flex flex-row items-start gap-3 text-left text-sm">
          <div className="shrink-0 w-1/3 max-w-[200px]">
            <p className="text-base-content/80">Garage WebUI-NG</p>
          </div>
          <div className="flex-1 truncate">
            <p className="truncate">{config?.version ?? "unknown"}</p>
          </div>
        </div>

        {update?.updateAvailable && update.latest ? (
          <p className="text-sm mt-2">
            Update available: {update.latest}
            {update.url ? (
              <>
                {" — "}
                <a
                  href={update.url}
                  target="_blank"
                  rel="noreferrer"
                  className="link"
                >
                  view release
                </a>
              </>
            ) : null}
            {update.canSelfUpdate ? (
              <>
                {" — "}
                <Button
                  size="sm"
                  color="primary"
                  disabled={applyUpdate.isPending}
                  onClick={onUpdateNow}
                >
                  {applyUpdate.isPending ? "Updating…" : "Update now"}
                </Button>
              </>
            ) : null}
          </p>
        ) : null}

        {update?.canSelfUpdate && update?.updateAvailable ? (
          <div className="mt-1">
            <Checkbox
              label="Restart the service automatically after installing"
              checked={restartAfterInstall}
              disabled={applyUpdate.isPending}
              onChange={(e) => setRestartAfterInstall(e.target.checked)}
            />
            <p className="text-xs text-base-content/60 mt-1">
              The service manager must be configured to restart the service
              automatically, or the console will stay down until it is
              restarted manually.
            </p>
          </div>
        ) : null}

        {applyResult ? (
          <p className="text-sm mt-2">
            Update to {applyResult.version} staged. A restart is required to
            apply it — the running process is still serving{" "}
            {config?.version ?? "the previous version"} until then.
          </p>
        ) : null}

        {update?.updateCommand ? (
          <div className="mt-2">
            <p className="text-sm text-base-content/60">
              Download the release binary first, then:
            </p>
            <div className="relative">
              <code className="block whitespace-pre-wrap break-all rounded bg-base-200 p-2 text-xs pr-10">
                {update.updateCommand}
              </code>
              <Button
                icon={Copy}
                className="absolute right-0 top-0"
                color="ghost"
                aria-label="Copy update command"
                onClick={() => copyToClipboard(update.updateCommand || "")}
              />
            </div>
          </div>
        ) : update?.deployment === "managed" ? (
          <p className="text-sm text-base-content/60 mt-2">
            This deployment is updated from outside the app — replace the
            container image or the binary and restart the service.
          </p>
        ) : null}

        {update && !update.enabled ? (
          <p className="text-sm text-base-content/60 mt-2">
            Update checks are off. Set UPDATE_CHECK_ENABLED=true to enable
            them.
          </p>
        ) : null}

        {update?.checkFailed ? (
          <p className="text-sm text-base-content/60 mt-2">
            Could not reach GitHub to check for updates.
          </p>
        ) : null}
      </Card.Body>
    </Card>
  );
};

export default AboutTab;
