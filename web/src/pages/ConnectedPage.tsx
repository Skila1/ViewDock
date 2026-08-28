import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { useAuth } from "@/store/auth";

export function ConnectedPage() {
  const { system } = useAuth();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const ids = useQuery({ queryKey: ["identities"], queryFn: api.listIdentities });
  const discord = (ids.data ?? []).find((i) => i.provider === "discord");
  const err = params.get("error");
  const linked = params.get("linked") === "1";

  return (
    <div className="mx-auto max-w-xl space-y-4">
      <h1 className="text-lg font-semibold">Connected services</h1>
      <p className="text-sm text-dim">Optional Discord sign-in. Configure it in Admin → Discord.</p>
      {linked ? <p className="text-xs text-accent">Discord linked.</p> : null}
      {err ? <p className="text-xs text-danger">{err}</p> : null}
      <div className="rounded-md border border-line p-4">
        <h2 className="text-sm font-medium">Discord</h2>
        {discord ? (
          <p className="mt-2 text-sm">
            Linked as {discord.provider_username || discord.provider_user_id}
          </p>
        ) : (
          <p className="mt-2 text-sm text-dim">Not linked.</p>
        )}
        <div className="mt-3 flex gap-2">
          {system?.discord_configured ? (
            <a href="/api/v1/auth/discord?link=1" className="btn-green rounded-full px-4 py-1.5 text-sm">
              {discord ? "Re-link Discord" : "Link Discord"}
            </a>
          ) : (
            <p className="text-xs text-dim">Discord sign-in is not configured on this server.</p>
          )}
          {discord ? (
            <button
              type="button"
              className="rounded-full border border-line px-4 py-1.5 text-sm"
              onClick={async () => {
                await api.unlinkDiscord();
                await qc.invalidateQueries({ queryKey: ["identities"] });
              }}
            >
              Unlink
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
