import { useCallback, useEffect, useRef, useState } from "react";
import {
  Maximize,
  Loader2,
  Minimize,
  Pause,
  Play,
  SkipForward,
  Volume2,
  VolumeX,
  X,
} from "lucide-react";
import { api, ApiError } from "@/api/api";
import { nativeHlsSupported } from "@/api/profile";
import { cn } from "@/lib/cn";
import { enterNativeFullscreen } from "@/lib/device";
import { formatClock } from "@/lib/format";
import { usePlayerStore } from "@/store/player";
import type { ItemKind, PlaybackSession } from "@/types/api.gen";
import { attachSession, SessionGoneError, type AttachHandle } from "./attachMedia";
import { reducePlayer, type PlayerEvent, type PlayerPhase } from "./playerMachine";
import { canSeekInWindow, seekableBounds } from "./seekWindow";
import { WatchTogetherOverlay } from "./watchTogether/WatchTogetherOverlay";
import { useWatchTogether } from "./watchTogether/useWatchTogether";

type Props = {
  itemKind: ItemKind;
  itemId: string;
  startMs?: number;
  title?: string;
  togetherCode?: string;
  shareToken?: string;
  guestItem?: { kind: string; id: string };
  onEnded?: () => void;
  onClose?: () => void;
};

export function Player({
  itemKind,
  itemId,
  startMs = 0,
  title,
  togetherCode,
  shareToken,
  guestItem,
  onEnded,
  onClose,
}: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const attachRef = useRef<AttachHandle | null>(null);
  const sessionRef = useRef<PlaybackSession | null>(null);
  const phaseRef = useRef<PlayerPhase>("idle");
  const resumeRef = useRef(startMs);
  const qualityRef = useRef<string | undefined>(undefined);

  const { phase, session, setPhase, setSession, setResumeMs, reset } = usePlayerStore();
  const [showUi, setShowUi] = useState(true);
  const [muted, setMuted] = useState(false);
  const [fs, setFs] = useState(false);
  const [pos, setPos] = useState(0);
  const [dur, setDur] = useState(0);
  const [err, setErr] = useState<string | null>(null);
  const hideTimer = useRef<number>(0);
  const goneAt = useRef(0);
  const seekTimer = useRef<number>(0);
  const pendingSeekRef = useRef<number | null>(null);
  const attachBusyRef = useRef(false);
  const originRef = useRef(startMs);
  const [buffering, setBuffering] = useState(true);

  const bump = useCallback((ev: PlayerEvent) => {
    const next = reducePlayer(phaseRef.current, ev);
    phaseRef.current = next;
    setPhase(next);
    return next;
  }, [setPhase]);

  const teardownAttach = () => {
    attachRef.current?.destroy();
    attachRef.current = null;
  };

  const endRemote = async () => {
    const id = sessionRef.current?.id;
    sessionRef.current = null;
    if (id) {
      try {
        await api.endSession(id);
      } catch {
        /* ignore */
      }
    }
  };

  const createAndAttach = useCallback(
    async (reason: "START" | "QUALITY" | "GONE") => {
      const video = videoRef.current;
      if (!video) return;
      if (reason === "GONE") {
        const now = Date.now();
        if (now - goneAt.current < 2500) return;
        goneAt.current = now;
      }
      if (attachBusyRef.current && reason === "QUALITY") {
        return;
      }
      if (reason === "QUALITY") bump("QUALITY");
      else if (reason === "GONE") bump("GONE");
      else bump("START");
      attachBusyRef.current = true;
      setBuffering(true);
      setErr(null);
      try {
        const startAt = Math.floor(pendingSeekRef.current ?? resumeRef.current);
        pendingSeekRef.current = null;
        resumeRef.current = startAt;
        const replaceId = sessionRef.current?.id;
        await endRemote();
        teardownAttach();
        const sess = await api.createSession({
          item_kind: itemKind,
          item_id: itemId,
          start_ms: startAt,
          quality: qualityRef.current,
          replace_session_id: replaceId,
        });
        sessionRef.current = sess;
        originRef.current = sess.seekable_from_ms ?? startAt;
        setSession(sess);
        if (sess.duration_ms && sess.duration_ms > 0) setDur(sess.duration_ms);
        setPos(originRef.current);
        bump("SESSION_CREATED");
        attachRef.current = await attachSession(video, sess, () => {
          const ms = originRef.current + (video.currentTime || 0) * 1000;
          resumeRef.current = ms;
          pendingSeekRef.current = ms;
          setResumeMs(ms);
          void createAndAttach("GONE");
        });
        bump("ATTACHED");
        const later = pendingSeekRef.current;
        if (later != null && Math.abs(later - originRef.current) > 2500) {
          attachBusyRef.current = false;
          resumeRef.current = later;
          void createAndAttach("QUALITY");
          return;
        }
        pendingSeekRef.current = null;
        try {
          await video.play();
          setBuffering(false);
          bump("PLAY");
        } catch (playErr) {
          if (playErr instanceof DOMException && playErr.name === "NotAllowedError") {
            setBuffering(false);
            bump("PAUSE");
            return;
          }
          if (playErr instanceof DOMException && playErr.name === "NotSupportedError") {
            await new Promise((r) => setTimeout(r, 800));
            try {
              await video.play();
              setBuffering(false);
              bump("PLAY");
              return;
            } catch (retryErr) {
              const detail = retryErr instanceof Error ? retryErr.message : "not supported";
              setErr(`This stream could not start (${detail}). Exit and try again.`);
              bump("ERROR");
              return;
            }
          }
          bump("PAUSE");
        }
      } catch (e) {
        if (e instanceof SessionGoneError || (e instanceof ApiError && e.status === 410)) {
          attachBusyRef.current = false;
          void createAndAttach("GONE");
          return;
        }
        setBuffering(false);
        setErr(e instanceof Error ? e.message : "playback failed");
        bump("ERROR");
      } finally {
        attachBusyRef.current = false;
      }
    },
    [bump, itemId, itemKind, setPhase, setResumeMs, setSession],
  );

  useEffect(() => {
    resumeRef.current = startMs;
    setResumeMs(startMs);
    void createAndAttach("START");
    return () => {
      const sess = sessionRef.current;
      const video = videoRef.current;
      const origin = sess?.seekable_from_ms ?? originRef.current;
      const lastPos = sess && video ? Math.floor(origin + (video.currentTime || 0) * 1000) : 0;
      const lastDur = sess && video ? Math.floor(sess.duration_ms || (video.duration || 0) * 1000) : 0;
      teardownAttach();
      void (async () => {
        if (sess) {
          try {
            await api.putProgress(sess.id, { position_ms: lastPos, duration_ms: lastDur });
          } catch {
            /* ignore */
          }
        }
        await endRemote();
      })();
      bump("DESTROY");
      reset();
    };
    // start once per item
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [itemKind, itemId]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const onTime = () => {
      if (pendingSeekRef.current != null || attachBusyRef.current) return;
      const ms = originRef.current + video.currentTime * 1000;
      setPos(ms);
      resumeRef.current = ms;
    };
    const onDur = () => {
      const probed = sessionRef.current?.duration_ms ?? 0;
      if (probed > 0) {
        setDur(probed);
        return;
      }
      const next = video.duration * 1000;
      if (Number.isFinite(next) && next > 0) setDur(next);
    };
    const onPlay = () => {
      if (!attachBusyRef.current) setBuffering(false);
      bump("PLAY");
    };
    const onPause = () => bump("PAUSE");
    const onWaiting = () => setBuffering(true);
    const onCanPlay = () => {
      if (!attachBusyRef.current && pendingSeekRef.current == null) setBuffering(false);
    };
    const onEnded = () => {
      bump("ENDED");
      onEnded?.();
    };
    video.addEventListener("timeupdate", onTime);
    video.addEventListener("durationchange", onDur);
    video.addEventListener("play", onPlay);
    video.addEventListener("pause", onPause);
    video.addEventListener("waiting", onWaiting);
    video.addEventListener("canplay", onCanPlay);
    video.addEventListener("playing", onCanPlay);
    video.addEventListener("ended", onEnded);
    return () => {
      video.removeEventListener("timeupdate", onTime);
      video.removeEventListener("durationchange", onDur);
      video.removeEventListener("play", onPlay);
      video.removeEventListener("pause", onPause);
      video.removeEventListener("waiting", onWaiting);
      video.removeEventListener("canplay", onCanPlay);
      video.removeEventListener("playing", onCanPlay);
      video.removeEventListener("ended", onEnded);
    };
  }, [bump, onEnded]);

  useEffect(() => {
    const id = window.setInterval(() => {
      const sess = sessionRef.current;
      const video = videoRef.current;
      if (!sess || !video || phaseRef.current !== "playing") return;
      const origin = sess.seekable_from_ms ?? 0;
      void api.putProgress(sess.id, {
        position_ms: Math.floor(origin + video.currentTime * 1000),
        duration_ms: Math.floor(sess.duration_ms || (video.duration || 0) * 1000),
      });
    }, 10_000);
    return () => window.clearInterval(id);
  }, []);

  const reveal = () => {
    setShowUi(true);
    window.clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(() => setShowUi(false), 2500);
  };

  useEffect(() => {
    reveal();
    return () => window.clearTimeout(hideTimer.current);
  }, []);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !nativeHlsSupported()) return;
    video.setAttribute("playsinline", "");
    video.setAttribute("webkit-playsinline", "");
    video.setAttribute("x-webkit-airplay", "allow");
    const onFs = () => setFs(true);
    const onFsEnd = () => setFs(false);
    video.addEventListener("webkitbeginfullscreen", onFs);
    video.addEventListener("webkitendfullscreen", onFsEnd);
    return () => {
      video.removeEventListener("webkitbeginfullscreen", onFs);
      video.removeEventListener("webkitendfullscreen", onFsEnd);
    };
  }, []);

  const togglePlay = () => {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) void video.play();
    else video.pause();
  };

  const seek = (ms: number) => {
    const video = videoRef.current;
    if (!video) return;
    const origin = originRef.current;
    const movieDur = sessionRef.current?.duration_ms ?? 0;
    const target = Math.max(0, movieDur > 0 ? Math.min(ms, movieDur) : ms);
    setPos(target);
    resumeRef.current = target;
    const bounds = !attachBusyRef.current ? seekableBounds(video) : {};
    const inWindow = canSeekInWindow({
      targetMs: target,
      originMs: origin,
      seekableStartSec: bounds.startSec,
      seekableEndSec: bounds.endSec,
    });
    if (!inWindow || attachBusyRef.current) {
      pendingSeekRef.current = target;
      setBuffering(true);
      if (attachBusyRef.current) return;
      window.clearTimeout(seekTimer.current);
      seekTimer.current = window.setTimeout(() => {
        void createAndAttach("QUALITY");
      }, 350);
      return;
    }
    pendingSeekRef.current = null;
    video.currentTime = Math.max(0, (target - origin) / 1000);
  };

  const changeQuality = (q: string) => {
    qualityRef.current = q;
    const video = videoRef.current;
    resumeRef.current = originRef.current + (video?.currentTime || 0) * 1000;
    void createAndAttach("QUALITY");
  };

  const wt = useWatchTogether({
    code: togetherCode,
    shareToken,
    guestItem,
    onRemote: (remote) => {
      const video = videoRef.current;
      if (!video) return;
      if (Math.abs(video.currentTime * 1000 - remote.positionMs) > 1500) {
        video.currentTime = remote.positionMs / 1000;
      }
      if (remote.playing && video.paused) void video.play();
      if (!remote.playing && !video.paused) video.pause();
    },
  });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
      if (e.code === "Space") {
        e.preventDefault();
        togglePlay();
      }
      if (e.key === "f") void videoRef.current?.parentElement?.requestFullscreen();
      if (e.key === "ArrowRight") seek(pos + 10_000);
      if (e.key === "ArrowLeft") seek(Math.max(0, pos - 10_000));
      if (e.key === "m") {
        const v = videoRef.current;
        if (v) {
          v.muted = !v.muted;
          setMuted(v.muted);
        }
      }
      if (e.key === "Escape") onClose?.();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, pos]);

  const duration = (session?.duration_ms && session.duration_ms > 0)
    ? session.duration_ms
    : (Number.isFinite(dur) && dur > 0 ? dur : 0);
  const qualities = session?.qualities ?? [];
  const attaching =
    phase === "creatingSession" ||
    phase === "attaching" ||
    phase === "switchingQuality" ||
    phase === "recreating";
  const showSpinner = !err && (buffering || attaching);
  const applePlayer = nativeHlsSupported();

  return (
    <div
      className="relative h-dvh w-dvw overflow-hidden bg-black overscroll-none"
      onMouseMove={reveal}
      onClick={reveal}
    >
      <video
        ref={videoRef}
        className="h-full w-full object-contain"
        playsInline
        controls={applePlayer}
        preload="auto"
        onClick={applePlayer ? undefined : (e) => {
          e.stopPropagation();
          togglePlay();
        }}
      />

      {togetherCode || wt.room ? (
        <WatchTogetherOverlay
          code={togetherCode || wt.room?.code}
          title={wt.invite?.title || title}
          peers={wt.peers}
          sync={wt.sync}
          error={wt.error}
          guest={Boolean(shareToken)}
          invitePath={wt.sharePath}
        />
      ) : null}

      <div
        className={cn(
          "pointer-events-none absolute inset-0 transition-opacity",
          showUi ? "opacity-100" : "opacity-0",
        )}
      >
        <div
          className="absolute inset-x-0 top-0 flex items-center justify-between bg-gradient-to-b from-black/70 to-transparent px-4 py-3"
          style={{ paddingTop: "max(0.75rem, var(--sat))" }}
        >
          <div className="pointer-events-auto min-w-0">
            <p className="truncate text-sm font-medium text-white">{title}</p>
          </div>
          {onClose ? (
            <button
              type="button"
              className="pointer-events-auto tap rounded-full bg-black/50 p-2 text-white"
              aria-label="Exit player"
              onClick={(e) => {
                e.stopPropagation();
                onClose();
              }}
            >
              <X size={20} />
            </button>
          ) : null}
        </div>

        <div
          className="pointer-events-auto absolute inset-x-0 bg-gradient-to-t from-black/80 to-transparent px-4 pt-10"
          style={{
            bottom: applePlayer ? "max(3.25rem, calc(var(--sab) + 2.75rem))" : 0,
            paddingBottom: applePlayer ? "0.5rem" : "max(1rem, var(--sab))",
          }}
        >
          <input
            type="range"
            min={0}
            max={Math.max(1, duration)}
            value={Math.min(pos, duration)}
            onChange={(e) => seek(Number(e.target.value))}
            className="mb-3 h-8 w-full accent-[var(--accent)]"
          />
          <div className="flex flex-wrap items-center gap-3">
            {applePlayer ? null : (
              <button type="button" onClick={togglePlay} className="tap text-white" aria-label="Play pause">
                {phase === "playing" ? <Pause size={22} /> : <Play size={22} />}
              </button>
            )}
            <span className="text-xs tabular-nums text-white/80">
              {formatClock(pos)} / {formatClock(duration)}
            </span>
            {applePlayer ? null : (
              <button
                type="button"
                className="tap text-white"
                onClick={() => {
                  const v = videoRef.current;
                  if (!v) return;
                  v.muted = !v.muted;
                  setMuted(v.muted);
                }}
                aria-label="Mute"
              >
                {muted ? <VolumeX size={18} /> : <Volume2 size={18} />}
              </button>
            )}
            <div className="ml-auto flex items-center gap-2">
              {session?.next_episode ? (
                <button
                  type="button"
                  className="tap flex items-center gap-1 rounded border border-white/20 px-2 text-xs text-white"
                  onClick={() => onEnded?.()}
                >
                  <SkipForward size={14} /> Next
                </button>
              ) : null}
              {qualities.length > 0 ? (
                <select
                  className="tap rounded border border-white/20 bg-black/40 px-2 text-xs"
                  value={qualityRef.current ?? qualities[0]}
                  onChange={(e) => changeQuality(e.target.value)}
                >
                  {qualities.map((q) => (
                    <option key={q} value={q}>
                      {q}
                    </option>
                  ))}
                </select>
              ) : null}
              <button
                type="button"
                className="tap text-white"
                aria-label="Fullscreen"
                onClick={() => {
                  const video = videoRef.current;
                  if (applePlayer && video && enterNativeFullscreen(video)) {
                    setFs(true);
                    return;
                  }
                  const root = video?.parentElement;
                  if (!document.fullscreenElement) {
                    void root?.requestFullscreen();
                    setFs(true);
                  } else {
                    void document.exitFullscreen();
                    setFs(false);
                  }
                }}
              >
                {fs ? <Minimize size={18} /> : <Maximize size={18} />}
              </button>
            </div>
          </div>
        </div>
      </div>

      {showSpinner ? (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
          <Loader2
            className="h-12 w-12 animate-spin text-white/85"
            strokeWidth={2}
            aria-label="Loading"
          />
        </div>
      ) : null}

      {err ? (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-black/70">
          <p className="max-w-md text-center text-sm text-danger">{err}</p>
          <div className="flex gap-3">
            <button type="button" className="rounded-md bg-accent px-3 py-1.5 text-sm text-black" onClick={() => void createAndAttach("START")}>
              Retry
            </button>
            {onClose ? (
              <button type="button" className="rounded-md border border-white/30 px-3 py-1.5 text-sm text-white" onClick={onClose}>
                Exit
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
