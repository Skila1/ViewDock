import { FormEvent, useEffect, useState } from "react";
import { Navigate, useNavigate } from "react-router";
import { Logo } from "@/components/brand/Logo";
import { api } from "@/api/api";
import { useAuth } from "@/store/auth";
import type { ContentType, DetectResult, Library } from "@/types/api.gen";

const STEPS = ["admin", "library", "ffmpeg", "tmdb", "scan", "done"] as const;
type Step = (typeof STEPS)[number];

export function SetupPage() {
  const { system, boot } = useAuth();
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>("admin");
  const [err, setErr] = useState("");
  const [library, setLibrary] = useState<Library | null>(null);
  const [ffmpeg, setFfmpeg] = useState<DetectResult | null>(null);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [bootstrapToken, setBootstrapToken] = useState("");
  const [bootstrapRequired, setBootstrapRequired] = useState(false);
  const [libName, setLibName] = useState("Library");
  const [libPath, setLibPath] = useState("");
  const [mediaDir, setMediaDir] = useState(system?.media_dir ?? "");
  const [contentType, setContentType] = useState<ContentType>("mixed");
  const [tmdbKey, setTmdbKey] = useState("");

  useEffect(() => {
    void api.setupStatus().then((s) => {
      if (s.step && STEPS.includes(s.step as Step)) setStep(s.step as Step);
      setBootstrapRequired(Boolean(s.bootstrap_required));
      if (s.media_dir) {
        setMediaDir(s.media_dir);
        setLibPath((prev) => prev || s.media_dir || "");
      }
    });
  }, []);

  if (system && !system.setup_needed) return <Navigate to="/" replace />;

  const fail = (e: unknown) => setErr(e instanceof Error ? e.message : "setup failed");

  const onAdmin = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    try {
      await api.setupAdmin({ username, password, display_name: displayName, bootstrap_token: bootstrapToken });
      await api.ensureCsrf();
      setStep("library");
    } catch (e2) {
      fail(e2);
    }
  };

  const onLibrary = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    if (!contentType) {
      setErr("content_type is required");
      return;
    }
    try {
      const lib = await api.setupLibrary({
        name: libName,
        path: libPath || mediaDir,
        content_type: contentType,
      });
      setLibrary(lib);
      setStep("ffmpeg");
    } catch (e2) {
      fail(e2);
    }
  };

  const onFfmpeg = async () => {
    setErr("");
    try {
      setFfmpeg(await api.setupFfmpeg());
    } catch (e2) {
      fail(e2);
    }
  };

  return (
    <div className="mx-auto flex min-h-dvh max-w-lg flex-col justify-center bg-bg p-6">
      <Logo className="mb-4 h-12 w-12" />
      <p className="mb-1 text-xs font-semibold uppercase tracking-[0.08em] text-accent">First-run setup</p>
      <h1 className="mb-4 text-xl font-semibold tracking-tight">ViewDock</h1>
      <ol className="mb-5 flex flex-wrap gap-2 text-[11px] text-dim">
        {STEPS.map((s) => (
          <li key={s} className={s === step ? "text-accent" : undefined}>
            {s}
          </li>
        ))}
      </ol>
      {err ? <p className="mb-3 text-sm text-danger">{err}</p> : null}

      {step === "admin" ? (
        <form onSubmit={onAdmin} className="space-y-3">
          <input className="w-full" placeholder="Admin username" value={username} onChange={(e) => setUsername(e.target.value)} required />
          <input className="w-full" placeholder="Display name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
          <input className="w-full" type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          {bootstrapRequired ? (
            <>
              <input
                className="w-full"
                type="password"
                autoComplete="off"
                placeholder="Setup bootstrap token"
                value={bootstrapToken}
                onChange={(e) => setBootstrapToken(e.target.value)}
                required
              />
              <p className="text-xs text-dim">
                Token is in your config directory as <code>setup.token</code>, or set <code>VD_SETUP_TOKEN</code>. It is not shown in the API.
              </p>
            </>
          ) : null}
          <button className="btn-green rounded-full px-4 py-2 text-sm" type="submit">
            Create admin
          </button>
        </form>
      ) : null}

      {step === "library" ? (
        <form onSubmit={onLibrary} className="space-y-3">
          <input className="w-full" placeholder="Library name" value={libName} onChange={(e) => setLibName(e.target.value)} required />
          <p className="rounded-md border border-line bg-raised px-3 py-2 text-sm text-dim">
            Folder: <span className="text-fg">{mediaDir || libPath || "/media"}</span>
            <span className="mt-1 block text-xs">
              Using the folder already mounted into ViewDock. Put files in your host media directory (the compose <code className="text-accent">./media</code> volume).
            </span>
          </p>
          <label className="block text-xs text-dim">
            content_type
            <select
              className="mt-1 w-full"
              value={contentType}
              onChange={(e) => setContentType(e.target.value as ContentType)}
              required
            >
              <option value="movies">movies</option>
              <option value="tv">tv</option>
              <option value="mixed">mixed</option>
            </select>
          </label>
          <button className="btn-green rounded-full px-4 py-2 text-sm" type="submit">
            Save library
          </button>
        </form>
      ) : null}

      {step === "ffmpeg" ? (
        <div className="space-y-3">
          <button type="button" className="btn-green rounded-full px-4 py-2 text-sm" onClick={() => void onFfmpeg()}>
            Detect FFmpeg
          </button>
          {ffmpeg ? (
            <pre className="overflow-auto rounded bg-raised p-3 text-[11px] text-dim">
              {JSON.stringify(ffmpeg, null, 2)}
            </pre>
          ) : null}
          <button type="button" className="block text-sm text-accent" onClick={() => setStep("tmdb")}>
            Continue
          </button>
        </div>
      ) : null}

      {step === "tmdb" ? (
        <form
          className="space-y-3"
          onSubmit={async (e) => {
            e.preventDefault();
            try {
              await api.setupTmdb({ api_key: tmdbKey, skip: !tmdbKey });
              setStep("scan");
            } catch (e2) {
              fail(e2);
            }
          }}
        >
          <input className="w-full" placeholder="TMDB API key (optional)" value={tmdbKey} onChange={(e) => setTmdbKey(e.target.value)} />
          <div className="flex gap-2">
            <button className="btn-green rounded-full px-4 py-2 text-sm" type="submit">
              {tmdbKey ? "Save key" : "Skip"}
            </button>
            <button
              type="button"
              className="rounded-md border border-line px-4 py-2 text-sm"
              onClick={async () => {
                await api.setupTmdb({ skip: true });
                setStep("scan");
              }}
            >
              Skip
            </button>
          </div>
        </form>
      ) : null}

      {step === "scan" ? (
        <div className="space-y-3">
          <p className="text-sm text-dim">Scan {library?.name ?? "library"} now.</p>
          <button
            type="button"
            className="btn-green rounded-full px-4 py-2 text-sm"
            onClick={async () => {
              try {
                if (library?.id) await api.setupScan({ library_id: library.id });
                setStep("done");
              } catch (e2) {
                fail(e2);
              }
            }}
          >
            Start scan
          </button>
          <button type="button" className="block text-sm text-dim" onClick={() => setStep("done")}>
            Skip scan
          </button>
        </div>
      ) : null}

      {step === "done" ? (
        <button
          type="button"
          className="btn-green rounded-full px-4 py-2 text-sm"
          onClick={async () => {
            await api.setupComplete();
            await boot();
            navigate("/");
          }}
        >
          Open library
        </button>
      ) : null}
    </div>
  );
}
