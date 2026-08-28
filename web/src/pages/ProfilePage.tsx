import { FormEvent, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { useAuth } from "@/store/auth";

export function ProfilePage() {
  const { me, boot } = useAuth();
  const qc = useQueryClient();
  const prefs = useQuery({ queryKey: ["prefs"], queryFn: api.getPreferences });
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: api.listSessions });
  const [display, setDisplay] = useState(me?.display_name ?? "");
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [pin, setPin] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const saveProfile = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    setMsg("");
    try {
      await api.patchMe({ display_name: display });
      await boot();
      setMsg("Profile saved");
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "save failed");
    }
  };

  const savePrefs = async (e: FormEvent) => {
    e.preventDefault();
    if (!prefs.data) return;
    setErr("");
    try {
      await api.putPreferences(prefs.data);
      await qc.invalidateQueries({ queryKey: ["prefs"] });
      setMsg("Playback preferences saved");
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "save failed");
    }
  };

  return (
    <div className="mx-auto max-w-xl space-y-8">
      <div>
        <h1 className="text-lg font-semibold">Profile</h1>
        <p className="text-sm text-dim">
          Signed in as {me?.username}
          {me?.roles?.length ? ` · ${me.roles.join(", ")}` : null}
        </p>
        <Link to="/settings/connected" className="mt-1 inline-block text-sm text-accent">
          Connected services
        </Link>
      </div>

      <form onSubmit={saveProfile} className="space-y-2">
        <h2 className="text-sm font-medium">Display name</h2>
        <input className="w-full" value={display} onChange={(e) => setDisplay(e.target.value)} />
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          Save name
        </button>
      </form>

      <form onSubmit={savePrefs} className="space-y-2">
        <h2 className="text-sm font-medium">Playback</h2>
        <label className="block text-xs text-dim">
          Audio language
          <input
            className="mt-1 w-full"
            value={prefs.data?.audio_lang ?? ""}
            onChange={(e) =>
              qc.setQueryData(["prefs"], { ...prefs.data, audio_lang: e.target.value })
            }
          />
        </label>
        <label className="block text-xs text-dim">
          Subtitle language
          <input
            className="mt-1 w-full"
            value={prefs.data?.subtitle_lang ?? ""}
            onChange={(e) =>
              qc.setQueryData(["prefs"], { ...prefs.data, subtitle_lang: e.target.value })
            }
          />
        </label>
        <label className="block text-xs text-dim">
          Subtitles
          <select
            className="mt-1 w-full"
            value={prefs.data?.subtitle_mode ?? "auto"}
            onChange={(e) =>
              qc.setQueryData(["prefs"], { ...prefs.data, subtitle_mode: e.target.value })
            }
          >
            <option value="auto">Auto</option>
            <option value="always">Always</option>
            <option value="off">Off</option>
          </select>
        </label>
        <label className="flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            checked={prefs.data?.autoplay ?? true}
            onChange={(e) =>
              qc.setQueryData(["prefs"], { ...prefs.data, autoplay: e.target.checked })
            }
          />
          Autoplay next episode
        </label>
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          Save playback
        </button>
      </form>

      <form
        className="space-y-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setErr("");
          try {
            await api.changePassword({ current, next });
            setCurrent("");
            setNext("");
            setMsg("Password updated. Other sessions were signed out.");
          } catch (e2) {
            setErr(e2 instanceof Error ? e2.message : "password failed");
          }
        }}
      >
        <h2 className="text-sm font-medium">{me?.has_password ? "Change password" : "Set a password"}</h2>
        {me?.has_password ? (
          <input
            className="w-full"
            type="password"
            placeholder="Current password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        ) : (
          <p className="text-xs text-dim">You signed in with Discord. Set a password to also use username login.</p>
        )}
        <input
          className="w-full"
          type="password"
          placeholder="New password (8+ characters)"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          required
        />
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          Update password
        </button>
      </form>

      <form
        className="space-y-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setErr("");
          try {
            if (pin) {
              await api.setPin(pin);
              setPin("");
              setMsg("PIN set. Idle lock is 15 minutes.");
            } else {
              await api.clearPin();
              setMsg("PIN cleared");
            }
            await boot();
          } catch (e2) {
            setErr(e2 instanceof Error ? e2.message : "pin failed");
          }
        }}
      >
        <h2 className="text-sm font-medium">PIN lock</h2>
        <p className="text-xs text-dim">Optional 4–8 digit PIN after idle. Leave empty and save to clear.</p>
        <input
          className="w-full"
          inputMode="numeric"
          placeholder={me?.has_pin ? "New PIN or empty to clear" : "PIN"}
          value={pin}
          onChange={(e) => setPin(e.target.value)}
        />
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          {pin ? "Set PIN" : "Clear PIN"}
        </button>
      </form>

      <div>
        <h2 className="mb-2 text-sm font-medium">Sessions</h2>
        <ul className="divide-y divide-line rounded-md border border-line">
          {(sessions.data ?? []).map((sess) => (
            <li key={sess.id} className="flex items-center justify-between gap-2 px-3 py-2 text-xs">
              <span>
                {sess.current ? <span className="text-accent">This device · </span> : null}
                {sess.ip || "unknown IP"}
                <span className="ml-2 text-dim">{sess.user_agent?.slice(0, 48)}</span>
              </span>
              {!sess.current ? (
                <button
                  type="button"
                  className="text-danger"
                  onClick={async () => {
                    await api.revokeSession(sess.id);
                    await qc.invalidateQueries({ queryKey: ["sessions"] });
                  }}
                >
                  Revoke
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      </div>

      {msg ? <p className="text-xs text-accent">{msg}</p> : null}
      {err ? <p className="text-xs text-danger">{err}</p> : null}
    </div>
  );
}
