import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";
import {
  Maximize,
  Loader2,
  Minimize,
  Pause,
  Play,
  Radio,
  SkipForward,
  Volume2,
  VolumeX,
  X,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "@/api/api";
import { failedCodecs, nativeHlsSupported, rememberCodecFailure } from "@/api/profile";
import { cn } from "@/lib/cn";
import { enterAvkitDetailed, enterNativeFullscreen, exitNativeFullscreen, isIOSDevice, isNativeFullscreen, restoreMmsRemotePlaybackLock } from "@/lib/device";
import { formatClock } from "@/lib/format";
import { flush, report, setJourneyContext } from "@/lib/journey";
import { noteAttach, noteCurrentTimeWrite, noteLogical, noteMedia, noteMediaDom, noteUserControl, readAttachTrace, setAttachMeta, viewDockPause } from "@/playback/attachTrace";
import { buildPlaybackErrorReport, mediaErrorMessage, type ErrorStage } from "@/playback/errorReport";
import { isVodOnDemand, logicalPositionMs, seekReplacesSession, selectPlaybackEngine, sessionOriginMs } from "@/playback/controller";
import { debugPlaybackEnabled, fullscreenStrategy, movieDurationMs, type PlaybackEngine } from "@/playback/policy";
import { usePlayerStore } from "@/store/player";
import type { ItemKind, PlaybackSession } from "@/types/api.gen";
import { diagnosticsApi } from "@/api/diagnostics";
import { attachSession, CodecFallbackError, SessionGoneError, type AttachHandle } from "./attachMedia";
import { SessionTelemetry } from "./sessionTelemetry";
import { PlaybackDiagnostics } from "./PlaybackDiagnostics";
import { StatsOverlay } from "./StatsOverlay";
import { EndCard, UpNextCard, type EndCardTitle } from "./UpNext";
import { prepareNext, takePrepared } from "./nextEpisode";
import type { CacheStats } from "./bufferCache";
import { PlayerErrorPanel, type ReportStatus } from "./PlayerErrorPanel";
import { PlayerSettingsMenu, type MenuOption } from "./PlayerSettingsMenu";
import { PauseOverlay, type NowPlayingInfo } from "./PauseOverlay";
import { SubtitleOverlay } from "./SubtitleOverlay";
import { Forward10, Replay10 } from "./icons";
import { SeekPreview } from "./SeekPreview";
import { activeCues, parseCues, subtitleBurnsIn, subtitleLabel, type Cue } from "@/playback/subtitles";
import { reducePlayer, type PlayerEvent, type PlayerPhase } from "./playerMachine";
import { shouldExitFullscreen } from "./fullscreenToggle";
import { canSeekInWindow, generatedMediaEndSec, seekableBounds, vodMovieSeekable } from "./seekWindow";
import { hardwareAccelerationHelp } from "./hardwareAcceleration";
import { WatchTogetherOverlay } from "./watchTogether/WatchTogetherOverlay";
import { panelVisible } from "./watchTogether/partySync";
import { useWatchTogether } from "./watchTogether/useWatchTogether";

type Props = {
  itemKind: ItemKind;
  itemId: string;
  startMs?: number;
  title?: string;
  /** Title details shown when playback has been paused for a while. */
  info?: NowPlayingInfo;
  togetherCode?: string;
  shareToken?: string;
  guestItem?: { kind: string; id: string };
  onEnded?: () => void;
  onClose?: () => void;
  /** Leaves the watch party; when set, the party panel offers "Leave party". */
  onLeaveParty?: () => void;
  /** Titles offered when a movie ends. */
  related?: EndCardTitle[];
};

const SPEEDS: MenuOption<number>[] = [
  { value: 0.25, label: "0.25x" },
  { value: 0.5, label: "0.5x" },
  { value: 0.75, label: "0.75x" },
  { value: 1, label: "Normal" },
  { value: 1.25, label: "1.25x" },
  { value: 1.5, label: "1.5x" },
  { value: 1.75, label: "1.75x" },
  { value: 2, label: "2x" },
];
// Automatic recoveries from a dead stream within RECOVER_WINDOW_MS before
// the error panel is shown.
const RECOVER_LIMIT = 3;
const RECOVER_WINDOW_MS = 120_000;

const PAUSE_OVERLAY_MS = 10_000;
// Longest a Jellyfin start waits for its head start in the buffer cache.
const PREBUFFER_MAX_MS = 25_000;

/** The buffer cache's stored ranges as a seek bar layer, a lighter gray than the empty track. */
export function cacheGradient(ranges: [number, number][], originMs: number, totalMs: number): string {
  if (!ranges.length || totalMs <= 0) return "linear-gradient(transparent, transparent)";
  const shade = "rgb(255 255 255 / 30%)";
  const pct = (sec: number) => `${Math.min(100, Math.max(0, ((originMs + sec * 1000) / totalMs) * 100)).toFixed(3)}%`;
  const stops = ["transparent 0"];
  for (const [a, b] of ranges) stops.push(`transparent ${pct(a)}`, `${shade} ${pct(a)}`, `${shade} ${pct(b)}`, `transparent ${pct(b)}`);
  return `linear-gradient(to right, ${stops.join(", ")})`;
}

function qualityLabel(q: string): string {
  if (q === "auto") return "Auto";
  return /^\d+$/.test(q) ? `${q}p` : q;
}

function withStoken(url: string, stoken?: string): string {
  if (!stoken || /[?&]stoken=/.test(url)) return url;
  return `${url}${url.includes("?") ? "&" : "?"}stoken=${encodeURIComponent(stoken)}`;
}

const ctrlBtn =
  "tap grid h-11 w-11 place-items-center rounded-full sm:h-12 sm:w-12 sm:[&_svg]:scale-[1.15] text-white/90 outline-none transition duration-150 hover:bg-white/[0.12] hover:text-white active:scale-90 focus-visible:ring-2 focus-visible:ring-white/70";

export function Player({
  itemKind,
  itemId,
  startMs = 0,
  title,
  info,
  togetherCode,
  shareToken,
  guestItem,
  onEnded,
  onClose,
  onLeaveParty,
  related,
}: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const attachRef = useRef<AttachHandle | null>(null);
  const sessionRef = useRef<PlaybackSession | null>(null);
  const phaseRef = useRef<PlayerPhase>("idle");
  const resumeRef = useRef(startMs);
  const qualityRef = useRef<string | undefined>(undefined);
  const sourceRef = useRef<string | undefined>(undefined);

  const { phase, session, setPhase, setSession, setResumeMs, reset } = usePlayerStore();
  const [showUi, setShowUi] = useState(true);
  const [muted, setMuted] = useState(false);
  const [volume, setVolume] = useState(1);
  const [quality, setQuality] = useState("auto");
  const subtitleRef = useRef<number | null>(null);
  const [subtitle, setSubtitle] = useState<number | null>(null);
  const lastSubtitleRef = useRef<number | null>(null);
  const audioRef = useRef<number | undefined>(undefined);
  const [audioIndex, setAudioIndex] = useState<number | undefined>(undefined);
  const [statsOpen, setStatsOpen] = useState(false);
  const [cacheStats, setCacheStats] = useState<CacheStats | null>(null);
  const [upNextDismissed, setUpNextDismissed] = useState(false);
  const [movieEnded, setMovieEnded] = useState(false);
  const recoverRef = useRef({ count: 0, at: 0 });
  const nextPreparedRef = useRef(false);
  const profile = Boolean(!shareToken);
  const prefs = useQuery({ queryKey: ["prefs"], queryFn: api.getPreferences, staleTime: 60_000, enabled: profile });
  const autoplayRef = useRef(true);
  autoplayRef.current = prefs.data?.autoplay ?? true;
  const [cues, setCues] = useState<Cue[]>([]);
  const [shownCues, setShownCues] = useState<Cue[]>([]);
  const [speed, setSpeed] = useState(1);
  const [settingsOpen, setSettingsOpenState] = useState(false);
  const settingsOpenRef = useRef(false);
  const menuClosedAtRef = useRef(0);
  const lastFailAtRef = useRef(0);
  const retryGateRef = useRef(0);
  const [idlePaused, setIdlePaused] = useState(false);
  const idleTimer = useRef<number>(0);
  const [bufferedMs, setBufferedMs] = useState(0);
  const [hoverSeek, setHoverSeek] = useState<{ ms: number; pct: number } | null>(null);
  const [nudge, setNudge] = useState<{ dir: -1 | 1; key: number } | null>(null);
  const [fs, setFs] = useState(false);
  const [pageFs, setPageFs] = useState(false);
  const [pos, setPos] = useState(0);
  const [dur, setDur] = useState(0);
  const movieDurRef = useRef(0);
  const [err, setErr] = useState<string | null>(null);
  const [errDetails, setErrDetails] = useState("");
  const [errReport, setErrReport] = useState<ReportStatus>({ state: "sending" });
  const errShownRef = useRef(false);
  const hideTimer = useRef<number>(0);
  const goneAt = useRef(0);
  const seekTimer = useRef<number>(0);
  const pendingSeekRef = useRef<number | null>(null);
  const attachBusyRef = useRef(false);
  const attachRunRef = useRef(0);
  const originRef = useRef(startMs);
  const [buffering, setBuffering] = useState(true);
  const bufferingRef = useRef(true);
  bufferingRef.current = buffering;
  const partyControlRef = useRef<((action: "play" | "pause" | "seek", positionMs: number) => void) | null>(null);
  const attachedAtRef = useRef(0);
  const lastStablePosRef = useRef(startMs);
  const fsExitAtRef = useRef(0);
  const playingAtFsExitRef = useRef(false);
  const userPausedRef = useRef(false);
  const suppressReplaceUntilRef = useRef(0);
  const genRef = useRef(0);
  const engineRef = useRef<PlaybackEngine | null>(null);
  const [engine, setEngine] = useState<PlaybackEngine | null>(null);
  const debug = debugPlaybackEnabled();
  const telemetryRef = useRef<SessionTelemetry | null>(null);
  if (!telemetryRef.current) telemetryRef.current = new SessionTelemetry(diagnosticsApi.sendSessionTelemetry);
  const telemetry = telemetryRef.current;
  const attemptAtRef = useRef(0);
  const attemptReasonRef = useRef<"START" | "QUALITY" | "GONE">("START");
  const firstFrameRef = useRef(false);
  const stallAtRef = useRef(0);

  useEffect(() => {
    const id = window.setInterval(() => void telemetry.flush(), 12_000);
    return () => window.clearInterval(id);
  }, [telemetry]);

  useEffect(() => {
    setJourneyContext({ item_kind: itemKind, item_id: itemId });
    report("play.start", { kind: itemKind, id: itemId, start_ms: startMs, title });
    return () => {
      report("play.end", { kind: itemKind, id: itemId });
      setJourneyContext({ session_id: undefined });
      void flush(true);
    };
  }, [itemKind, itemId, startMs, title]);

  const bump = useCallback((ev: PlayerEvent) => {
    const next = reducePlayer(phaseRef.current, ev);
    phaseRef.current = next;
    setPhase(next);
    return next;
  }, [setPhase]);

  const reopenRef = useRef<(why: string) => void>(() => undefined);
  // A reopened session stays paused when the viewer had paused.
  const stayPausedRef = useRef(false);

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

  const failPlayback = useCallback(
    (message: string, code: string, stage: ErrorStage) => {
      const video = videoRef.current;
      const trace = readAttachTrace(video);
      const page = typeof window === "undefined" ? "" : window.location.pathname;
      const built = buildPlaybackErrorReport({
        message,
        code,
        stage,
        itemKind,
        itemId,
        title,
        session: sessionRef.current,
        engine: engineRef.current,
        positionMs: video ? logicalPositionMs(originRef.current, video.currentTime || 0) : undefined,
        video: video
          ? {
              readyState: video.readyState,
              networkState: video.networkState,
              currentTime: video.currentTime,
              duration: video.duration,
              paused: video.paused,
              error: video.error ? { code: video.error.code, message: video.error.message } : null,
            }
          : null,
        trace,
        page: shareToken ? page.split(shareToken).join("[share]") : page,
        userAgent: navigator.userAgent,
        mse: typeof MediaSource !== "undefined",
        nativeHls: nativeHlsSupported(),
      });
      errShownRef.current = true;
      lastFailAtRef.current = Date.now();
      setBuffering(false);
      setErr(message);
      setErrDetails(built.text);
      setErrReport({ state: "sending" });
      bump("ERROR");
      report("play.error", { code, stage, session_id: sessionRef.current?.id ?? "" });
      api.reportClientError(built.request).then(
        (res) => setErrReport({ state: "sent", id: res.id }),
        () => setErrReport({ state: "failed" }),
      );
    },
    [bump, itemId, itemKind, shareToken, title],
  );

  const createAndAttach = useCallback(
    async (reason: "START" | "RETRY" | "QUALITY" | "GONE") => {
      const video = videoRef.current;
      if (!video) return;
      // A failed start used to be entered again immediately, which hammered the
      // media source. Automatic starts wait, and Retry itself cannot repeat
      // faster than once every 1.5s.
      if (reason === "START" && Date.now() - lastFailAtRef.current < 1500) return;
      if (reason === "RETRY") {
        const now = Date.now();
        if (now < retryGateRef.current) return;
        retryGateRef.current = now + 1500;
      }
      if (attachBusyRef.current && reason !== "QUALITY") return;
      if (reason === "GONE") {
        const now = Date.now();
        if (now - goneAt.current < 2500) return;
        goneAt.current = now;
      }
      if (attachBusyRef.current && reason === "QUALITY") {
        return;
      }
      if (reason === "QUALITY" && Date.now() < suppressReplaceUntilRef.current) {
        noteAttach(video, "session_replace_suppressed_after_fs", `start_ms=${Math.floor(pendingSeekRef.current ?? resumeRef.current)}`);
        return;
      }
      if (reason === "QUALITY") bump("QUALITY");
      else if (reason === "GONE") bump("GONE");
      else bump("START");
      attachBusyRef.current = true;
      const gen = genRef.current;
      // A nested follow-up call (pending seek, GONE) owns the busy flag from
      // here on, so this call must not clear it when it unwinds.
      const run = ++attachRunRef.current;
      setBuffering(true);
      setErr(null);
      errShownRef.current = false;
      try {
        const startAt = Math.floor(pendingSeekRef.current ?? resumeRef.current);
        pendingSeekRef.current = null;
        resumeRef.current = startAt;
        const replaceId = sessionRef.current?.id;
        const attempt = reason === "RETRY" ? "START" : reason;
        noteAttach(video, "session_replace_begin", `reason=${attempt} outgoing=${replaceId ?? ""} start_ms=${startAt}`);
        if (reason !== "GONE" || attemptReasonRef.current !== "GONE") attemptAtRef.current = Date.now();
        attemptReasonRef.current = attempt;
        await telemetry.flush();
        await endRemote();
        teardownAttach();
        if (genRef.current !== gen) return;
        // A next episode prepared near the end of the last one is taken over:
        // its opening is already in the buffer cache.
        const prepared = reason === "START" && !shareToken && !togetherCode ? takePrepared(itemKind, itemId) : null;
        const sess =
          prepared ??
          (await api.createSession({
            item_kind: itemKind,
            item_id: itemId,
            start_ms: startAt,
            quality: qualityRef.current,
            audio_index: audioRef.current,
            subtitle_index: subtitleRef.current ?? undefined,
            replace_session_id: replaceId,
            source: sourceRef.current,
          }));
        if (prepared) noteAttach(video, "prepared_session", prepared.id);
        if (genRef.current !== gen) {
          try {
            await api.endSession(sess.id);
          } catch {
            /* superseded */
          }
          return;
        }
        sessionRef.current = sess;
        telemetry.bind(sess.id);
        // The server may have applied the profile's tracks and quality.
        if (sess.subtitle_index !== undefined) {
          subtitleRef.current = sess.subtitle_index ?? null;
          setSubtitle(sess.subtitle_index ?? null);
        }
        if (typeof sess.audio_index === "number" && sess.audio_index > 0) {
          audioRef.current = sess.audio_index;
          setAudioIndex(sess.audio_index);
        }
        if (sess.quality && !qualityRef.current) {
          qualityRef.current = sess.quality;
          setQuality(sess.quality);
        }
        firstFrameRef.current = false;
        stallAtRef.current = 0;
        telemetry.record("source_selected", {
          reason: reason.toLowerCase(),
          mode: sess.decision?.mode ?? "",
          delivery: sess.delivery ?? "",
        });
        if (reason === "GONE") telemetry.record("failover", { reason: "node_unavailable", from: replaceId ?? "", to: sess.id });
        setJourneyContext({ session_id: sess.id, item_kind: itemKind, item_id: itemId });
        report("play.session", {
          id: sess.id,
          reason,
          mode: sess.decision?.mode,
          playback: sess.decision?.playback,
          delivery: sess.delivery,
          start_ms: startAt,
          replace: replaceId ?? "",
        });
        noteAttach(video, "session_created", `reason=${reason} id=${sess.id} replace=${replaceId ?? ""}`);
        const vod = isVodOnDemand(sess);
        originRef.current = sessionOriginMs(sess, startAt);
        setAttachMeta(video, { originMs: originRef.current, sessionId: sess.id });
        setSession(sess);
        const predicted = selectPlaybackEngine(sess, { hlsJsSupported: true, nativeHls: nativeHlsSupported() }, isIOSDevice());
        engineRef.current = predicted;
        setEngine(predicted);
        if (sess.duration_ms && sess.duration_ms > 0) {
          movieDurRef.current = sess.duration_ms;
          setDur(sess.duration_ms);
        }
        setPos(originRef.current);
        bump("SESSION_CREATED");
        attachRef.current = await attachSession(
          video,
          sess,
          () => {
            const ms = originRef.current + (video.currentTime || 0) * 1000;
            resumeRef.current = ms;
            pendingSeekRef.current = ms;
            setResumeMs(ms);
            void createAndAttach("GONE");
          },
          (eng) => {
            if (genRef.current !== gen) return;
            engineRef.current = eng;
            setEngine(eng);
          },
          (movieMs) => {
            if (isVodOnDemand(sessionRef.current)) return;
            pendingSeekRef.current = movieMs;
            resumeRef.current = movieMs;
            window.clearTimeout(seekTimer.current);
            seekTimer.current = window.setTimeout(() => {
              seekTimer.current = 0;
              void createAndAttach("QUALITY");
            }, 350);
          },
          (detail) => {
            if (genRef.current !== gen || errShownRef.current) return;
            telemetry.record("error", { code: "HLS_FATAL", detail });
            // A dead stream reopens at the same position before the viewer
            // sees an error. A stream taken over by another device only
            // gets one try, so two devices do not take it back and forth.
            const elsewhere = detail.includes("started somewhere else");
            const now = Date.now();
            if (now - recoverRef.current.at > RECOVER_WINDOW_MS) recoverRef.current.count = 0;
            if (recoverRef.current.count < (elsewhere ? 1 : RECOVER_LIMIT)) {
              recoverRef.current.count += 1;
              recoverRef.current.at = now;
              const ms = originRef.current + (video.currentTime || 0) * 1000;
              resumeRef.current = ms;
              pendingSeekRef.current = ms;
              setResumeMs(ms);
              noteAttach(video, "auto_recover", `try=${recoverRef.current.count} ${detail}`);
              telemetry.record("reconnect", { reason: detail, attempt: recoverRef.current.count });
              window.setTimeout(() => {
                if (genRef.current === gen) void createAndAttach("GONE");
              }, 1000 * recoverRef.current.count);
              return;
            }
            failPlayback(`Playback stopped: the stream failed (${detail}).`, "HLS_FATAL", "playback");
          },
          {
            cacheTitle: { kind: itemKind, id: itemId, quality: qualityRef.current },
            startSec: isVodOnDemand(sess) ? startAt / 1000 : undefined,
            onCodecFallback: (detail) => {
              if (genRef.current !== gen) return;
              telemetry.record("codec_error", { detail, fallback: "h264" });
              const codec = sess.remote_video?.codec ?? "";
              if (!codec || failedCodecs().has(codec)) {
                failPlayback(`Playback stopped: the stream failed (${detail}).`, "HLS_FATAL", "playback");
                return;
              }
              rememberCodecFailure(codec);
              const ms = originRef.current + (video.currentTime || 0) * 1000;
              resumeRef.current = ms;
              pendingSeekRef.current = ms;
              setResumeMs(ms);
              void createAndAttach("QUALITY");
            },
          },
        );
        if (genRef.current !== gen) {
          teardownAttach();
          return;
        }
        engineRef.current = attachRef.current.engine;
        setEngine(attachRef.current.engine);
        bump("ATTACHED");
        attachedAtRef.current = Date.now();
        lastStablePosRef.current = originRef.current;
        if (vod && startAt > 2500) {
          noteCurrentTimeWrite(video, startAt / 1000, "createAndAttach.vodResume", sess.id);
          video.currentTime = startAt / 1000;
        }
        const later = pendingSeekRef.current;
        if (later != null && Math.abs(later - originRef.current) > 2500) {
          if (vod) {
            noteCurrentTimeWrite(video, later / 1000, "createAndAttach.vodPendingSeek", sess.id);
            video.currentTime = later / 1000;
            pendingSeekRef.current = null;
          } else {
            attachBusyRef.current = false;
            resumeRef.current = later;
            void createAndAttach("QUALITY");
            return;
          }
        }
        pendingSeekRef.current = null;
        // Jellyfin streams download ahead into the buffer cache from the
        // start; playback begins once enough is stored to run without stalling.
        const prebuffer = attachRef.current?.prebuffer;
        if (stayPausedRef.current) {
          stayPausedRef.current = false;
          setBuffering(false);
          bump("PAUSE");
          return;
        }
        if (prebuffer) {
          await prebuffer(PREBUFFER_MAX_MS);
          if (genRef.current !== gen) return;
        }
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
              telemetry.record("startup_failed", { code: "NOT_SUPPORTED", detail });
              failPlayback(hardwareAccelerationHelp(), "NOT_SUPPORTED", "startup");
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
        // The browser claimed a decoder it does not have: once per tab, ask
        // the source for H.264 instead of failing.
        const failedCodec = sessionRef.current?.remote_video?.codec ?? "";
        if (e instanceof CodecFallbackError && failedCodec && !failedCodecs().has(failedCodec)) {
          rememberCodecFailure(failedCodec);
          telemetry.record("codec_error", { detail: e.message, fallback: "h264" });
          attachBusyRef.current = false;
          void createAndAttach("QUALITY");
          return;
        }
        const code = e instanceof ApiError ? (e.code ?? String(e.status)) : "ATTACH_FAILED";
        telemetry.record("startup_failed", { code });
        failPlayback(e instanceof Error ? e.message : "playback failed", code, "startup");
      } finally {
        if (genRef.current === gen && attachRunRef.current === run) attachBusyRef.current = false;
      }
    },
    [bump, failPlayback, itemId, itemKind, setPhase, setResumeMs, setSession, telemetry],
  );

  useEffect(() => {
    genRef.current += 1;
    // A create still running for the previous item is abandoned by the new
    // generation and never clears the flag itself.
    attachBusyRef.current = false;
    lastFailAtRef.current = 0;
    retryGateRef.current = 0;
    phaseRef.current = "idle";
    resumeRef.current = startMs;
    setResumeMs(startMs);
    void createAndAttach("START");
    return () => {
      genRef.current += 1;
      const sess = sessionRef.current;
      const video = videoRef.current;
      const origin = originRef.current;
      // React detaches the video before this cleanup runs, so the position
      // comes from the last time update, never from a missing element (that
      // reported 0 and erased the resume point).
      const lastPos = Math.floor(video && video.currentTime > 0 ? logicalPositionMs(origin, video.currentTime) : lastStablePosRef.current);
      const lastDur = Math.floor(sess?.duration_ms || movieDurRef.current || (video?.duration || 0) * 1000);
      teardownAttach();
      void (async () => {
        if (sess && lastPos > 0) {
          try {
            await api.putProgress(sess.id, { position_ms: lastPos, duration_ms: lastDur, event: "stop" }, { kind: itemKind, id: itemId });
          } catch {
            /* ignore */
          }
        }
        await telemetry.flush();
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
    const onBuffered = () => {
      const t = video.currentTime || 0;
      const b = video.buffered;
      for (let i = 0; i < b.length; i++) {
        if (b.start(i) <= t + 0.5 && b.end(i) >= t) {
          setBufferedMs(logicalPositionMs(originRef.current, b.end(i)));
          return;
        }
      }
      setBufferedMs(0);
    };
    const onTime = () => {
      if (attachBusyRef.current) return;
      if (pendingSeekRef.current != null) {
        if (seekTimer.current) return;
        // The reopen this seek waited for was dropped (busy, guarded or
        // cancelled): the video kept playing, so follow it again. A stale
        // target here froze the clock and made every skip start from it.
        pendingSeekRef.current = null;
      }
      const origin = originRef.current;
      const rel = video.currentTime || 0;
      const ms = logicalPositionMs(origin, rel);
      lastStablePosRef.current = ms;
      setPos(ms);
      resumeRef.current = ms;
      onBuffered();
    };
    const onDur = () => {
      const probed = sessionRef.current?.duration_ms ?? 0;
      if (probed > 0) {
        movieDurRef.current = probed;
        setDur(probed);
        return;
      }
      // Never adopt EVENT/live video.duration: that is why the slider
      // flashed ~30m then 2:50 then snapped back.
    };
    const onPlay = () => {
      if (!attachBusyRef.current) setBuffering(false);
      bump("PLAY");
    };
    const onPause = () => {
      bump("PAUSE");
      if (
        playingAtFsExitRef.current &&
        !userPausedRef.current &&
        fsExitAtRef.current > 0 &&
        Date.now() - fsExitAtRef.current < 1200
      ) {
        noteAttach(video, "webkit_pause_after_fs_resumed", `t=${video.currentTime.toFixed(3)}`);
        void video.play().then(() => bump("PLAY")).catch(() => {});
      }
    };
    const onWaiting = () => {
      setBuffering(true);
      bump("WAITING");
      if (firstFrameRef.current && !attachBusyRef.current && !video.seeking && stallAtRef.current === 0) {
        stallAtRef.current = Date.now();
        telemetry.record("stall", { position_ms: Math.floor(logicalPositionMs(originRef.current, video.currentTime || 0)) });
      }
    };
    const onPlaying = () => {
      const now = Date.now();
      if (!firstFrameRef.current && sessionRef.current) {
        firstFrameRef.current = true;
        const elapsed = now - attemptAtRef.current;
        telemetry.record("first_frame", { ttff_ms: elapsed, reason: attemptReasonRef.current.toLowerCase() });
        if (attemptReasonRef.current === "GONE") telemetry.record("recovered", { duration_ms: elapsed });
      }
      if (stallAtRef.current > 0) {
        telemetry.record("stall_end", { duration_ms: now - stallAtRef.current });
        stallAtRef.current = 0;
      }
    };
    const onCanPlay = () => {
      if (!attachBusyRef.current && pendingSeekRef.current == null) setBuffering(false);
      bump("CANPLAY");
    };
    const checkpoint = (event: "pause" | "seek" | "ended") => {
      const sess = sessionRef.current;
      if (!sess || attachBusyRef.current) return;
      void api
        .putProgress(
          sess.id,
          {
            position_ms: Math.floor(logicalPositionMs(originRef.current, video.currentTime || 0)),
            duration_ms: Math.floor(sess.duration_ms || (video.duration || 0) * 1000),
            event,
          },
          { kind: itemKind, id: itemId },
        )
        .catch(() => {});
    };
    const onPauseCheckpoint = () => {
      if (!video.ended) checkpoint("pause");
    };
    const onSeeked = () => {
      bump("SEEKED");
      checkpoint("seek");
    };
    const onVideoEnded = () => {
      bump("ENDED");
      checkpoint("ended");
      const next = Boolean(sessionRef.current?.next_episode);
      if (itemKind === "movie") setMovieEnded(true);
      // Without autoplay the Up Next card waits for a click.
      if (!next || autoplayRef.current) onEnded?.();
    };
    const domEv = [
      "play", "playing", "pause", "waiting", "stalled", "seeking", "seeked",
      "timeupdate", "durationchange", "loadedmetadata", "loadeddata", "canplay",
      "emptied", "abort", "suspend", "progress", "ratechange",
    ];
    const onDom = (e: Event) => noteMediaDom(video, e.type);
    video.addEventListener("timeupdate", onTime);
    video.addEventListener("progress", onBuffered);
    video.addEventListener("durationchange", onDur);
    video.addEventListener("play", onPlay);
    video.addEventListener("pause", onPause);
    video.addEventListener("pause", onPauseCheckpoint);
    video.addEventListener("waiting", onWaiting);
    video.addEventListener("canplay", onCanPlay);
    video.addEventListener("playing", onCanPlay);
    video.addEventListener("playing", onPlaying);
    video.addEventListener("seeked", onSeeked);
    video.addEventListener("ended", onVideoEnded);
    for (const name of domEv) video.addEventListener(name, onDom);
    return () => {
      video.removeEventListener("timeupdate", onTime);
      video.removeEventListener("progress", onBuffered);
      video.removeEventListener("durationchange", onDur);
      video.removeEventListener("play", onPlay);
      video.removeEventListener("pause", onPause);
      video.removeEventListener("pause", onPauseCheckpoint);
      video.removeEventListener("waiting", onWaiting);
      video.removeEventListener("canplay", onCanPlay);
      video.removeEventListener("playing", onCanPlay);
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("seeked", onSeeked);
      video.removeEventListener("ended", onVideoEnded);
      for (const name of domEv) video.removeEventListener(name, onDom);
    };
  }, [bump, onEnded, itemKind, itemId, telemetry]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    let timer = 0;
    const onError = () => {
      const sessionId = sessionRef.current?.id;
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        const mediaErr = video.error;
        if (!mediaErr || attachBusyRef.current || errShownRef.current) return;
        if (!sessionId || sessionRef.current?.id !== sessionId) return;
        const code = `MEDIA_ERR_${mediaErr.code}`;
        telemetry.record("error", { code });
        failPlayback(mediaErrorMessage(mediaErr.code), code, "playback");
      }, 1500);
    };
    video.addEventListener("error", onError);
    return () => {
      window.clearTimeout(timer);
      video.removeEventListener("error", onError);
    };
  }, [failPlayback, telemetry]);

  useEffect(() => {
    const tick = () => {
      const sess = sessionRef.current;
      const video = videoRef.current;
      // Paused and buffering players report too: the server ends sessions it
      // has not heard from in 45 seconds, and an external source stream is
      // not seen by the server, so a pause would otherwise end it.
      const phase = phaseRef.current;
      if (!sess || !video || !(phase === "playing" || phase === "paused" || phase === "buffering" || phase === "seeking")) return;
      const origin = originRef.current;
      const cache = attachRef.current?.stats?.();
      const q = video.getVideoPlaybackQuality?.();
      let bufferAhead = 0;
      for (let i = 0; i < video.buffered.length; i++) {
        if (video.buffered.start(i) <= video.currentTime + 0.5 && video.buffered.end(i) >= video.currentTime) bufferAhead = video.buffered.end(i) - video.currentTime;
      }
      void api.putProgress(sess.id, {
        position_ms: Math.floor(logicalPositionMs(origin, video.currentTime)),
        duration_ms: Math.floor(sess.duration_ms || (video.duration || 0) * 1000),
        stats: {
          paused: video.paused,
          buffer_ahead_ms: Math.round(bufferAhead * 1000),
          cache_ahead_ms: Math.round((cache?.aheadSec ?? 0) * 1000),
          cache_behind_ms: Math.round((cache?.behindSec ?? 0) * 1000),
          cache_bytes: cache?.bytes ?? 0,
          cache_hit_rate: cache?.hitRate ?? 0,
          throughput_bps: Math.round(cache?.throughputBps ?? 0),
          dropped_frames: q?.droppedVideoFrames ?? 0,
        },
      }).catch((e) => {
        // The session ended while nobody was watching (a long hidden tab):
        // open a new one at the same position instead of loading forever.
        if (e instanceof ApiError && e.status === 410 && sessionRef.current?.id === sess.id) reopenRef.current("progress_gone");
      });
    };
    const id = window.setInterval(tick, 10_000);
    // Hidden tabs run timers once a minute; report as soon as the viewer is back.
    const onVisible = () => {
      if (document.visibilityState === "visible") tick();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  useEffect(() => {
    let lastT = -1;
    const id = window.setInterval(() => {
      const video = videoRef.current;
      if (!video) return;
      const sess = sessionRef.current;
      const t = video.currentTime;
      const apple = video as HTMLVideoElement & { webkitDisplayingFullscreen?: boolean };
      if (video.paused && lastT >= 0 && Math.abs(t - lastT) > 0.25) {
        report("play.drift_while_paused", {
          from: lastT,
          to: t,
          delta: t - lastT,
          session_id: sess?.id,
          readyState: video.readyState,
          seeking: video.seeking,
        });
      }
      report("play.heartbeat", {
        session_id: sess?.id,
        currentTime: t,
        paused: video.paused,
        readyState: video.readyState,
        seeking: video.seeking,
        duration: video.duration,
        muted: video.muted,
        playbackRate: video.playbackRate,
        fullscreen: Boolean(document.fullscreenElement) || Boolean(apple.webkitDisplayingFullscreen),
        phase: phaseRef.current,
      });
      lastT = t;
    }, 2000);
    return () => window.clearInterval(id);
  }, []);

  const scheduleHide = () => {
    window.clearTimeout(hideTimer.current);
    hideTimer.current = window.setTimeout(() => {
      const v = videoRef.current;
      if (settingsOpenRef.current || !v || v.paused) return;
      setShowUi(false);
    }, 2800);
  };

  const armIdle = () => {
    window.clearTimeout(idleTimer.current);
    if (phaseRef.current !== "paused" || errShownRef.current || settingsOpenRef.current) return;
    idleTimer.current = window.setTimeout(() => {
      if (phaseRef.current !== "paused" || errShownRef.current || settingsOpenRef.current) return;
      setIdlePaused(true);
      setShowUi(false);
    }, PAUSE_OVERLAY_MS);
  };

  const reveal = () => {
    setShowUi(true);
    setIdlePaused(false);
    scheduleHide();
    armIdle();
  };

  const setSettingsOpen = (open: boolean) => {
    if (!open && settingsOpenRef.current) menuClosedAtRef.current = Date.now();
    settingsOpenRef.current = open;
    setSettingsOpenState(open);
    if (open) {
      window.clearTimeout(idleTimer.current);
      setShowUi(true);
    } else {
      scheduleHide();
      armIdle();
    }
  };

  useEffect(() => {
    reveal();
    return () => {
      window.clearTimeout(hideTimer.current);
      window.clearTimeout(idleTimer.current);
    };
    // mount only
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (phase === "paused") {
      armIdle();
      return;
    }
    window.clearTimeout(idleTimer.current);
    setIdlePaused(false);
    if (phase === "playing") scheduleHide();
  }, [phase]);

  const subtitleUrl = session?.urls?.subtitle ? withStoken(session.urls.subtitle, session.stoken) : "";
  useEffect(() => {
    setCues([]);
    setShownCues([]);
    if (!subtitleUrl) return;
    const ctl = new AbortController();
    fetch(subtitleUrl, { credentials: "include", signal: ctl.signal })
      .then((res) => (res.ok ? res.text() : Promise.reject(new Error(`subtitles ${res.status}`))))
      .then((text) => setCues(parseCues(text)))
      .catch((e: unknown) => {
        if (ctl.signal.aborted) return;
        const video = videoRef.current;
        if (video) noteAttach(video, "subtitles_failed", e instanceof Error ? e.message : "fetch failed");
      });
    return () => ctl.abort();
  }, [subtitleUrl]);

  useEffect(() => {
    if (!cues.length) return;
    let raf = 0;
    let last = "";
    const tick = () => {
      const video = videoRef.current;
      if (video) {
        const now = activeCues(cues, logicalPositionMs(originRef.current, video.currentTime || 0));
        const key = now.map((c) => c.startMs).join(",");
        if (key !== last) {
          last = key;
          setShownCues(now);
        }
      }
      raf = window.requestAnimationFrame(tick);
    };
    raf = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(raf);
  }, [cues]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    video.setAttribute("playsinline", "");
    video.setAttribute("webkit-playsinline", "");
    if (isIOSDevice()) video.setAttribute("x-webkit-airplay", "allow");
    const sync = () => {
      setFs(Boolean(document.fullscreenElement) || isNativeFullscreen(video) || pageFs);
    };
    const onFs = () => {
      report("play.fullscreen", { action: "enter", currentTime: video.currentTime, session_id: sessionRef.current?.id });
      noteLogical(video, "webkitbeginfullscreen", originRef.current, sessionRef.current?.id);
      setFs(true);
    };
    const onFsEnd = () => {
      playingAtFsExitRef.current = !video.paused;
      fsExitAtRef.current = Date.now();
      suppressReplaceUntilRef.current = Date.now() + 2500;
      window.clearTimeout(seekTimer.current);
      seekTimer.current = 0;
      report("play.fullscreen", { action: "exit", currentTime: video.currentTime, session_id: sessionRef.current?.id });
      noteLogical(video, "webkitendfullscreen", originRef.current, sessionRef.current?.id);
      noteAttach(video, "fs_exit_guard", `playing=${playingAtFsExitRef.current} suppress_replace_ms=2500`);
      if (engineRef.current === "hlsjs") restoreMmsRemotePlaybackLock(video);
      setFs(false);
      setPageFs(false);
    };
    video.addEventListener("webkitbeginfullscreen", onFs);
    video.addEventListener("webkitendfullscreen", onFsEnd);
    video.addEventListener("webkitpresentationmodechanged", sync);
    document.addEventListener("fullscreenchange", sync);
    return () => {
      video.removeEventListener("webkitbeginfullscreen", onFs);
      video.removeEventListener("webkitendfullscreen", onFsEnd);
      video.removeEventListener("webkitpresentationmodechanged", sync);
      document.removeEventListener("fullscreenchange", sync);
    };
  }, [pageFs]);

  const toggleFullscreen = (e: { preventDefault: () => void; stopPropagation: () => void }) => {
    const video = videoRef.current;
    const root = video?.parentElement;
    if (!video) return;
    // Enter AVKit before preventDefault so the touch is still a media gesture.
    if (
      shouldExitFullscreen({
        documentFs: Boolean(document.fullscreenElement),
        nativeFs: isNativeFullscreen(video),
        pageFs,
        chromeFs: fs,
      })
    ) {
      noteMedia(video, "fullscreen_exit_tap", sessionRef.current?.id);
      report("play.fullscreen", { action: "exit_tap", currentTime: video.currentTime, session_id: sessionRef.current?.id });
      if (document.fullscreenElement) void document.exitFullscreen();
      exitNativeFullscreen(video);
      noteMedia(video, "webkitExitFullscreen", sessionRef.current?.id);
      setPageFs(false);
      setFs(false);
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (fullscreenStrategy() === "avkit") {
      const apple = video as HTMLVideoElement & { webkitSupportsFullscreen?: boolean; webkitDisplayingFullscreen?: boolean };
      const result = enterAvkitDetailed(video);
      noteMedia(video, "fullscreen_tap", sessionRef.current?.id);
      report("play.fullscreen", {
        action: result.threw ? "enter_threw" : "tap",
        currentTime: video.currentTime,
        readyState: video.readyState,
        paused: video.paused,
        disableRemotePlayback: video.disableRemotePlayback,
        supports: apple.webkitSupportsFullscreen,
        displaying: apple.webkitDisplayingFullscreen,
        currentSrc: (video.currentSrc || "").slice(0, 120),
        playsinline: video.hasAttribute("playsinline"),
        threw: result.threw ?? null,
        session_id: sessionRef.current?.id,
      });
      if (result.ok) {
        noteMedia(video, "webkitEnterFullscreen", sessionRef.current?.id);
        setFs(true);
      } else {
        noteMedia(video, "webkitEnterFullscreen_failed", sessionRef.current?.id);
        report("play.fullscreen", { action: "enter_failed", currentTime: video.currentTime, threw: result.threw, session_id: sessionRef.current?.id });
      }
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    const req = root?.requestFullscreen?.() ?? video.requestFullscreen?.();
    if (req) {
      void req.then(() => setFs(true)).catch(() => {
        if (enterNativeFullscreen(video)) setFs(true);
        else {
          setPageFs(true);
          setFs(true);
        }
      });
      return;
    }
    if (enterNativeFullscreen(video)) setFs(true);
    else {
      setPageFs(true);
      setFs(true);
    }
  };

  const togglePlay = (via: "chrome" | "keyboard" | "video_click" = "chrome") => {
    const video = videoRef.current;
    if (!video) return;
    const movieMs = originRef.current + (video.currentTime || 0) * 1000;
    if (video.paused) {
      userPausedRef.current = false;
      report("play.resume", { via, currentTime: video.currentTime, session_id: sessionRef.current?.id });
      noteUserControl(video, "play", via);
      partyControlRef.current?.("play", movieMs);
      void video.play();
      return;
    }
    userPausedRef.current = true;
    report("play.pause", { via, currentTime: video.currentTime, session_id: sessionRef.current?.id });
    noteUserControl(video, "pause", via);
    partyControlRef.current?.("pause", movieMs);
    viewDockPause(video, `togglePlay:${via}`);
  };

  const seek = (ms: number, source = "unknown") => {
    const video = videoRef.current;
    if (!video) return;
    if (source === "slider" && Date.now() < suppressReplaceUntilRef.current) {
      noteAttach(video, "slider_seek_ignored_after_fs", `ms=${ms}`);
      return;
    }
    const origin = originRef.current;
    const movieDur = sessionRef.current?.duration_ms ?? 0;
    const target = Math.max(0, movieDur > 0 ? Math.min(ms, movieDur) : ms);
    if (source !== "party") partyControlRef.current?.("seek", target);
    setPos(target);
    resumeRef.current = target;
    lastStablePosRef.current = target;
    // After a failed start, party timeline updates kept opening new sessions,
    // hammering the source and replacing the error. Remember the position for
    // Retry instead.
    if (errShownRef.current && !sessionRef.current) {
      pendingSeekRef.current = target;
      return;
    }
    const bounds = !attachBusyRef.current ? seekableBounds(video) : {};
    const generatedEnd = attachRef.current?.generatedEndSec?.() ?? generatedMediaEndSec(video);
    const vod = isVodOnDemand(sessionRef.current);
    const inWindow = vod
      ? vodMovieSeekable({ targetMs: target, durationMs: movieDur })
      : canSeekInWindow({
          targetMs: target,
          originMs: origin,
          seekableStartSec: bounds.startSec,
          seekableEndSec: bounds.endSec,
          generatedEndSec: generatedEnd,
          ignoreSeekableStart: engineRef.current === "native-hls" && !vod,
        });
    report("play.seek", {
      source,
      target,
      inWindow,
      origin,
      generatedEnd,
      seekableEnd: bounds.endSec,
      currentTime: video.currentTime,
      session_id: sessionRef.current?.id,
    });
    if (seekReplacesSession(vod, inWindow) || (attachBusyRef.current && !vod)) {
      noteAttach(video, "vd_seek", JSON.stringify({ source, target, inWindow: false, origin }));
      pendingSeekRef.current = target;
      setBuffering(true);
      if (attachBusyRef.current) return;
      window.clearTimeout(seekTimer.current);
      seekTimer.current = window.setTimeout(() => {
        seekTimer.current = 0;
        void createAndAttach("QUALITY");
      }, 350);
      return;
    }
    bump("SEEK");
    pendingSeekRef.current = null;
    const rel = Math.max(0, (target - origin) / 1000);
    noteAttach(video, "vd_seek", JSON.stringify({ source, target, inWindow: true, rel }));
    noteCurrentTimeWrite(video, rel, "Player.seek", sessionRef.current?.id);
    video.currentTime = rel;
  };

  reopenRef.current = (why: string) => {
    const video = videoRef.current;
    if (!video || attachBusyRef.current) return;
    const ms = originRef.current + (video.currentTime || 0) * 1000;
    resumeRef.current = ms;
    pendingSeekRef.current = ms;
    setResumeMs(ms);
    noteAttach(video, "session_reopen", why);
    stayPausedRef.current = video.paused;
    void createAndAttach("GONE");
  };

  const changeQuality = (q: string) => {
    if (q === qualityRef.current) return;
    if (Date.now() < suppressReplaceUntilRef.current) {
      const video = videoRef.current;
      if (video) noteAttach(video, "quality_change_ignored_after_fs", q);
      return;
    }
    qualityRef.current = q;
    setQuality(q);
    if (profile) void api.putPreferences({ quality: q }).catch(() => undefined);
    const video = videoRef.current;
    resumeRef.current = originRef.current + (video?.currentTime || 0) * 1000;
    void createAndAttach("QUALITY");
  };

  const changeAudio = (index: number) => {
    if (index === audioRef.current) return;
    audioRef.current = index;
    setAudioIndex(index);
    if (profile) void api.setTitleTracks(itemKind, itemId, { audio_index: index }).catch(() => undefined);
    const video = videoRef.current;
    resumeRef.current = originRef.current + (video?.currentTime || 0) * 1000;
    void createAndAttach("QUALITY");
  };

  const changeSpeed = (rate: number) => {
    setSpeed(rate);
    if (profile) void api.putPreferences({ playback_rate: rate }).catch(() => undefined);
  };

  const changeSource = (id: string) => {
    if (id === (sourceRef.current ?? session?.source)) return;
    sourceRef.current = id;
    qualityRef.current = undefined;
    setQuality("auto");
    subtitleRef.current = null;
    setSubtitle(null);
    const video = videoRef.current;
    resumeRef.current = originRef.current + (video?.currentTime || 0) * 1000;
    void createAndAttach("QUALITY");
  };

  const changeSubtitle = (index: number | null) => {
    if (index === subtitleRef.current) return;
    if (subtitleRef.current != null) lastSubtitleRef.current = subtitleRef.current;
    subtitleRef.current = index;
    setSubtitle(index);
    if (profile) void api.setTitleTracks(itemKind, itemId, { subtitle_index: index ?? -1 }).catch(() => undefined);
    if (index == null) {
      setCues([]);
      setShownCues([]);
      if (session?.urls?.subtitle) return;
    }
    const video = videoRef.current;
    resumeRef.current = originRef.current + (video?.currentTime || 0) * 1000;
    void createAndAttach("QUALITY");
  };

  const skip = (dir: -1 | 1) => {
    // Skips while a seek is still opening its stream add up from its target;
    // otherwise they start from where the video is now.
    const video = videoRef.current;
    const inFlight = attachBusyRef.current || seekTimer.current !== 0;
    const from = inFlight || !video ? (pendingSeekRef.current ?? pos) : logicalPositionMs(originRef.current, video.currentTime || 0);
    seek(Math.max(0, from + dir * 10_000), dir < 0 ? "skip_back" : "skip_forward");
    setNudge({ dir, key: Date.now() });
  };

  const partyApply = (playing: boolean, positionMs: number | null) => {
    const video = videoRef.current;
    if (!video) return;
    if (positionMs != null) seek(positionMs, "party");
    if (playing && video.paused) {
      userPausedRef.current = false;
      void video.play().catch(() => {});
    }
    if (!playing && !video.paused) {
      video.playbackRate = 1;
      viewDockPause(video, "watchtogether.remote_pause");
    }
  };

  const wt = useWatchTogether({
    code: togetherCode,
    shareToken,
    guestItem,
    getLocal: () => {
      const video = videoRef.current;
      if (!video) return null;
      const phaseNow = phaseRef.current;
      const loading = attachBusyRef.current || phaseNow === "creatingSession" || phaseNow === "attaching" || phaseNow === "recreating";
      return {
        positionMs: originRef.current + (video.currentTime || 0) * 1000,
        playing: !video.paused,
        buffering: bufferingRef.current || pendingSeekRef.current != null,
        ready: !loading,
      };
    },
    onTimeline: (t) => {
      const video = videoRef.current;
      if (!video) return;
      const movieMs = pendingSeekRef.current ?? originRef.current + (video.currentTime || 0) * 1000;
      const far = Math.abs(movieMs - t.positionMs) > 1000;
      video.playbackRate = 1;
      partyApply(t.playing, far ? t.positionMs : null);
    },
    onCorrection: (c) => {
      const video = videoRef.current;
      if (!video) return;
      if (c.action !== "ok") telemetry.record("drift_correction", { drift_ms: Math.round(c.driftMs), action: c.action });
      if (c.action === "seek") {
        video.playbackRate = 1;
        partyApply(c.playing, c.targetMs);
        return;
      }
      video.playbackRate = c.action === "rate" && !video.paused ? c.rate : 1;
    },
  });
  partyControlRef.current = togetherCode && wt.status === "synced" ? wt.control : null;
  const [panelLocal, setPanelLocal] = useState<"open" | "closed" | null>(null);
  useEffect(() => setPanelLocal(null), [wt.panel]);
  const inParty = Boolean(togetherCode || wt.room);
  const partyNotice = wt.status === "ended" && Boolean(wt.error);
  const panelOpen = inParty && (partyNotice || panelVisible(wt.panel, wt.isHost, panelLocal));
  const canTogglePanel = inParty && (wt.isHost || wt.panel === "everyone");

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const rate = inParty ? 1 : speed;
    video.defaultPlaybackRate = rate;
    video.playbackRate = rate;
  }, [speed, inParty, session?.id]);

  // Desktop media keys: Space/K play-pause, J/L and arrows seek 10 s, Up and
  // Down change the volume, M mute, F fullscreen, C captions, I stats, N
  // next episode, 0 to 9 jump to that tenth, Esc exit.
  const keyRef = useRef<(e: KeyboardEvent) => void>(() => undefined);
  keyRef.current = (e: KeyboardEvent) => {
    if (settingsOpenRef.current || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.target instanceof HTMLTextAreaElement) return;
    if (e.target instanceof HTMLInputElement && e.target.type !== "range") return;
    const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    const v = videoRef.current;
    const handled = () => {
      e.preventDefault();
      reveal();
    };
    if (e.code === "Space" || key === "k") {
      handled();
      togglePlay("keyboard");
    } else if (key === "ArrowRight" || key === "ArrowLeft" || key === "j" || key === "l") {
      handled();
      skip(key === "ArrowRight" || key === "l" ? 1 : -1);
    } else if ((key === "ArrowUp" || key === "ArrowDown") && v) {
      handled();
      const next = Math.min(1, Math.max(0, Math.round((v.volume + (key === "ArrowUp" ? 0.05 : -0.05)) * 100) / 100));
      v.volume = next;
      v.muted = next === 0;
      setVolume(next);
      setMuted(next === 0);
    } else if (key === "m" && v) {
      v.muted = !v.muted;
      setMuted(v.muted);
    } else if (key === "f" && !isIOSDevice()) {
      toggleFullscreen({ preventDefault: () => e.preventDefault(), stopPropagation: () => e.stopPropagation() });
    } else if (key === "c") {
      handled();
      const tracks = (sessionRef.current?.subtitles ?? []).filter((t) => typeof t.index === "number");
      if (subtitleRef.current != null) changeSubtitle(null);
      else if (tracks.length) changeSubtitle(lastSubtitleRef.current ?? (tracks[0].index as number));
    } else if (key === "i") {
      setStatsOpen((s) => !s);
    } else if (key === "n" && sessionRef.current?.next_episode) {
      handled();
      onEnded?.();
    } else if (/^[0-9]$/.test(key) && duration > 0) {
      handled();
      seek((Number(key) / 10) * duration, "keyboard");
    } else if (key === "Escape") {
      if (statsOpen) setStatsOpen(false);
      else onClose?.();
    }
  };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => keyRef.current(e);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // The profile's playback speed applies when playback starts.
  useEffect(() => {
    const rate = prefs.data?.playback_rate;
    if (rate && rate > 0) setSpeed(rate);
  }, [prefs.data?.playback_rate]);

  // Buffer cache state for the stats overlay and the seek bar, and the
  // next-episode prefetch once this one is cached to its end.
  useEffect(() => {
    const id = window.setInterval(() => {
      const stats = attachRef.current?.stats?.() ?? null;
      setCacheStats(stats);
      const sess = sessionRef.current;
      const video = videoRef.current;
      const next = sess?.next_episode;
      if (!stats || !next || !video || nextPreparedRef.current || togetherCode || shareToken) return;
      const total = (sess.duration_ms ?? 0) / 1000 || video.duration || 0;
      const left = total - (video.currentTime || 0);
      if (total > 0 && left < 240 && stats.aheadSec >= left - 3 && (stats.rate ?? 0) >= 1.3) {
        nextPreparedRef.current = true;
        noteAttach(video, "next_episode_prefetch", next.id);
        void prepareNext(next.id, qualityRef.current).catch(() => undefined);
      }
    }, 1000);
    return () => window.clearInterval(id);
  }, [togetherCode, shareToken]);

  const applePlayer = isIOSDevice();
  const duration = movieDurationMs(session, movieDurRef.current || dur);
  const seekMax = Math.max(1, duration);
  const seekValue = Math.min(pos, duration);
  const progressPct = duration > 0 ? Math.min(100, Math.max(0, (seekValue / seekMax) * 100)) : 0;
  const volumePct = (muted ? 0 : volume) * 100;
  const qualities = session?.qualities ?? [];
  const sources = session?.sources ?? [];
  const attaching =
    phase === "creatingSession" ||
    phase === "attaching" ||
    phase === "switchingQuality" ||
    phase === "recreating" ||
    phase === "buffering";
  const showSpinner = !err && (buffering || attaching);
  const bufferPct = duration > 0 && bufferedMs > 0 ? Math.min(100, (bufferedMs / seekMax) * 100) : 0;
  const subtitleTracks = session?.subtitles ?? [];
  const qualityGroup = {
    value: qualities.length > 0 ? quality : "auto",
    options: (qualities.length > 0 ? qualities : ["auto"]).map((q) => ({
      value: q,
      label: qualityLabel(q),
      hint: q === "auto" ? "Recommended" : undefined,
    })),
    onChange: changeQuality,
  };
  const serverGroup =
    sources.length > 0
      ? { value: session?.source ?? sources[0].id, options: sources.map((s) => ({ value: s.id, label: s.label })), onChange: changeSource }
      : { value: "default", options: [{ value: "default", label: "Default" }], onChange: () => undefined };
  const subtitleGroup = {
    value: subtitle,
    options:
      subtitleTracks.length > 0
        ? [
            { value: null as number | null, label: "Off" },
            ...subtitleTracks
              .filter((t) => typeof t.index === "number")
              .map((t, i) => ({ value: t.index as number, label: subtitleLabel(t, i + 1), hint: subtitleBurnsIn(t) ? "Burned in" : undefined })),
          ]
        : [],
    onChange: changeSubtitle,
    empty: "This title has no subtitles.",
  };
  const audioTracks = (session?.audio ?? []).filter((t) => typeof t.index === "number");
  const audioGroup =
    audioTracks.length > 1
      ? {
          value: audioIndex ?? (audioTracks[0].index as number),
          options: audioTracks.map((t, i) => ({
            value: t.index as number,
            label: [t.language?.toUpperCase(), t.title, t.codec?.toUpperCase()].filter(Boolean).join(" · ") || `Track ${i + 1}`,
          })),
          onChange: changeAudio,
        }
      : null;
  const speedGroup = {
    value: inParty ? 1 : speed,
    options: SPEEDS,
    onChange: changeSpeed,
    disabled: inParty ? "Party sync" : undefined,
  };
  const pauseInfo: NowPlayingInfo | null = info ?? (title ? { title } : null);

  return (
    <div
      className={cn(
        "relative h-dvh w-dvw overflow-hidden bg-black overscroll-none",
        pageFs && "player-page-fs",
      )}
      onMouseMove={applePlayer ? undefined : reveal}
      onClick={applePlayer ? undefined : reveal}
    >
      <video
        ref={videoRef}
        className="h-full w-full object-contain"
        preload="auto"
        onClick={applePlayer ? undefined : (e) => {
          e.stopPropagation();
          reveal();
          if (Date.now() - menuClosedAtRef.current < 400) return;
          togglePlay("video_click");
        }}
      />

      {pauseInfo && !applePlayer ? <PauseOverlay info={pauseInfo} visible={idlePaused && !err} /> : null}

      {applePlayer ? null : (
        <div
          aria-hidden
          className={cn(
            "pointer-events-none absolute inset-x-0 bottom-0 z-[5] h-44 bg-gradient-to-t from-black/90 via-black/55 to-transparent transition-opacity duration-300",
            showUi ? "opacity-100" : "opacity-0",
          )}
        />
      )}

      <SubtitleOverlay cues={subtitle == null ? [] : shownCues} lifted={showUi && !applePlayer} />

      {nudge ? (
        <div
          key={nudge.key}
          className={cn(
            "vd-nudge pointer-events-none absolute top-1/2 z-[7] -mt-12 grid h-24 w-24 place-items-center rounded-full bg-black/45 text-white backdrop-blur-sm",
            nudge.dir < 0 ? "left-[18%]" : "right-[18%]",
          )}
          onAnimationEnd={() => setNudge(null)}
          aria-hidden
        >
          <div className="flex flex-col items-center gap-0.5">
            {nudge.dir < 0 ? <Replay10 size={34} /> : <Forward10 size={34} />}
            <span className="text-[11px] font-semibold tracking-wide text-white/85">{nudge.dir < 0 ? "-10s" : "+10s"}</span>
          </div>
        </div>
      ) : null}

      {debug ? (
        <PlaybackDiagnostics video={videoRef.current} session={session} engine={engine} originMs={originRef.current} />
      ) : null}

      {statsOpen ? <StatsOverlay video={videoRef.current} session={session} engine={engine ?? ""} stats={cacheStats} onClose={() => setStatsOpen(false)} /> : null}

      {session?.next_episode && !inParty && !upNextDismissed && duration > 60_000 && duration - pos <= Math.max(25_000, (prefs.data?.upnext_seconds ?? 10) * 1000 + 5000) ? (
        <UpNextCard
          title={session.next_episode.title}
          seconds={autoplayRef.current && (prefs.data?.upnext_seconds ?? 10) > 0 ? (prefs.data?.upnext_seconds ?? 10) : null}
          onPlay={() => onEnded?.()}
          onDismiss={() => setUpNextDismissed(true)}
        />
      ) : null}

      {movieEnded && itemKind === "movie" && !inParty && related?.length ? <EndCard titles={related} onClose={onClose} /> : null}

      {panelOpen ? (
        <WatchTogetherOverlay
          code={togetherCode || wt.room?.code}
          title={wt.invite?.title || title}
          members={wt.members}
          memberId={wt.memberId}
          sync={wt.sync}
          status={wt.status}
          syncInfo={wt.syncInfo}
          isHost={wt.isHost}
          sharedControl={wt.sharedControl}
          onSharedControl={wt.setSharedControl}
          panel={wt.panel}
          onPanel={wt.setPanel}
          onClose={canTogglePanel ? () => setPanelLocal("closed") : undefined}
          onLeave={togetherCode ? onLeaveParty : undefined}
          error={wt.error}
          guest={Boolean(shareToken)}
          invitePath={wt.sharePath}
        />
      ) : null}

      {applePlayer ? (
        <>
          {onClose ? (
            <button
              type="button"
              className="pointer-events-auto tap absolute z-10 rounded-full bg-black/50 p-2 text-white"
              style={{
                top: "max(0.5rem, calc(var(--sat) + 0.15rem))",
                right: "0.75rem",
              }}
              aria-label="Exit player"
              onClick={(e) => {
                e.stopPropagation();
                onClose();
              }}
            >
              <X size={20} />
            </button>
          ) : null}
          {canTogglePanel ? (
            <button
              type="button"
              className={cn("pointer-events-auto tap absolute z-10 rounded-full bg-black/50 p-2", panelOpen ? "text-accent" : "text-white")}
              style={{ top: "max(0.5rem, calc(var(--sat) + 0.15rem))", right: onClose ? "3.75rem" : "0.75rem" }}
              aria-label={panelOpen ? "Hide Watch Together panel" : "Show Watch Together panel"}
              aria-pressed={panelOpen}
              onClick={(e) => {
                e.stopPropagation();
                setPanelLocal(panelOpen ? "closed" : "open");
              }}
            >
              <Radio size={20} />
            </button>
          ) : null}
          {/* Safari's own controls seek and show the time; a bar of ours
              on top of them hid its scrubber. */}
        </>
      ) : (
        <div
          className={cn(
            "pointer-events-none absolute inset-x-0 top-0 z-10 transition-opacity duration-300",
            showUi ? "opacity-100" : "opacity-0",
          )}
        >
          <div className="absolute inset-x-0 top-0 h-32 bg-gradient-to-b from-black/80 via-black/35 to-transparent" />
          <div
            className="relative flex items-center justify-between gap-4 px-4 py-3 sm:px-6"
            style={{ paddingTop: "max(0.75rem, var(--sat))" }}
          >
            <div className="min-w-0">
              <p className="truncate text-[15px] font-semibold text-white [text-shadow:0_1px_4px_rgba(0,0,0,0.6)] sm:text-[17px]">{title}</p>
              {info?.episode ? <p className="truncate text-[13px] text-white/65 sm:text-sm">{[info.season, info.episode].filter(Boolean).join(" · ")}</p> : null}
            </div>
            {onClose ? (
              <button
                type="button"
                className={cn(ctrlBtn, showUi ? "pointer-events-auto" : "pointer-events-none")}
                aria-label="Exit player"
                title="Exit (Esc)"
                onClick={(e) => {
                  e.stopPropagation();
                  onClose();
                }}
              >
                <X size={20} />
              </button>
            ) : null}
          </div>
        </div>
      )}

      {applePlayer ? null : (
        <div
          className={cn(
            "absolute inset-x-0 bottom-0 z-10 transition-opacity duration-300",
            showUi ? "pointer-events-auto opacity-100" : "pointer-events-none opacity-0",
          )}
          style={{ paddingBottom: "max(0.75rem, var(--sab))" }}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="relative px-3 sm:px-6">
            <div
              className="player-seek-wrap relative"
              onMouseMove={(e) => {
                const r = e.currentTarget.getBoundingClientRect();
                const pct = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
                setHoverSeek({ ms: pct * seekMax, pct: pct * 100 });
              }}
              onMouseLeave={() => setHoverSeek(null)}
            >
              {hoverSeek && duration > 0 ? (
                <div
                  className="pointer-events-none absolute bottom-full mb-1.5 flex -translate-x-1/2 flex-col items-center"
                  style={{ left: `clamp(${session?.urls?.preview ? "6rem" : "1.75rem"}, ${hoverSeek.pct}%, calc(100% - ${session?.urls?.preview ? "6rem" : "1.75rem"}))` }}
                >
                  {session?.urls?.preview ? <SeekPreview base={session.urls.preview} ms={hoverSeek.ms} /> : null}
                  <span className="rounded-md bg-black/85 px-2 py-1 text-[12px] font-semibold tabular-nums text-white shadow-lg ring-1 ring-white/10">
                    {formatClock(hoverSeek.ms)}
                  </span>
                </div>
              ) : null}
              <input
                type="range"
                min={0}
                max={seekMax}
                value={seekValue}
                aria-label="Seek"
                aria-valuetext={`${formatClock(pos)} of ${formatClock(duration)}`}
                onChange={(e) => seek(Number(e.target.value), "slider")}
                className="player-seek"
                style={
                  {
                    "--player-range-pct": `${progressPct}%`,
                    "--player-buffer-pct": `${bufferPct}%`,
                    ...(cacheStats && duration > 0 ? { "--player-cache-bg": cacheGradient(cacheStats.ranges, originRef.current, seekMax) } : {}),
                  } as CSSProperties
                }
              />
            </div>

            <div className="mt-1 flex items-center gap-0.5 sm:gap-1.5">
              <button
                type="button"
                onClick={() => togglePlay("chrome")}
                className={ctrlBtn}
                aria-label={phase === "playing" ? "Pause" : "Play"}
                title={phase === "playing" ? "Pause (Space)" : "Play (Space)"}
              >
                {phase === "playing" ? <Pause size={24} fill="currentColor" strokeWidth={0} /> : <Play size={24} fill="currentColor" strokeWidth={0} className="translate-x-px" />}
              </button>
              <button type="button" className={ctrlBtn} aria-label="Back 10 seconds" title="Back 10 seconds (Left arrow)" onClick={() => skip(-1)}>
                <Replay10 size={25} />
              </button>
              <button type="button" className={ctrlBtn} aria-label="Forward 10 seconds" title="Forward 10 seconds (Right arrow)" onClick={() => skip(1)}>
                <Forward10 size={25} />
              </button>

              <div className="group/vol flex items-center">
                <button
                  type="button"
                  className={ctrlBtn}
                  onClick={() => {
                    const v = videoRef.current;
                    if (!v) return;
                    v.muted = !v.muted;
                    setMuted(v.muted);
                  }}
                  aria-label={muted || volume === 0 ? "Unmute" : "Mute"}
                  title={muted || volume === 0 ? "Unmute (M)" : "Mute (M)"}
                >
                  {muted || volume === 0 ? <VolumeX size={22} /> : <Volume2 size={22} />}
                </button>
                <div className="w-0 overflow-hidden opacity-0 transition-all duration-200 ease-out group-focus-within/vol:w-24 group-focus-within/vol:opacity-100 group-hover/vol:w-24 group-hover/vol:opacity-100">
                  <input
                    type="range"
                    min={0}
                    max={1}
                    step={0.05}
                    value={muted ? 0 : volume}
                    aria-label="Volume"
                    className="player-range ml-1 h-8 w-[5.25rem]"
                    style={{ "--player-range-pct": `${volumePct}%` } as CSSProperties}
                    onChange={(e) => {
                      const v = videoRef.current;
                      const next = Number(e.target.value);
                      setVolume(next);
                      if (v) {
                        v.volume = next;
                        v.muted = next === 0;
                      }
                      setMuted(next === 0);
                    }}
                  />
                </div>
              </div>

              <span className="ml-2 whitespace-nowrap text-[13px] font-medium tabular-nums text-white sm:text-sm">
                {formatClock(pos)}
                <span className="text-white/50"> / {formatClock(duration)}</span>
              </span>

              <div className="ml-auto flex items-center gap-0.5 sm:gap-1.5">
                {session?.next_episode ? (
                  <button
                    type="button"
                    className="tap mr-1 hidden h-9 items-center gap-1.5 rounded-full bg-white/[0.12] px-3.5 text-[13px] font-semibold text-white outline-none transition hover:bg-white/[0.2] focus-visible:ring-2 focus-visible:ring-white/70 sm:flex"
                    onClick={() => onEnded?.()}
                    title={session.next_episode.title ? `Next: ${session.next_episode.title}` : "Next episode"}
                  >
                    <SkipForward size={15} fill="currentColor" strokeWidth={0} aria-hidden />
                    Next episode
                  </button>
                ) : null}
                {canTogglePanel ? (
                  <button
                    type="button"
                    className={cn(ctrlBtn, panelOpen && "text-accent hover:text-accent")}
                    aria-label={panelOpen ? "Hide Watch Together panel" : "Show Watch Together panel"}
                    title="Watch Together"
                    aria-pressed={panelOpen}
                    onClick={() => setPanelLocal(panelOpen ? "closed" : "open")}
                  >
                    <Radio size={21} />
                  </button>
                ) : null}
                <PlayerSettingsMenu
                  open={settingsOpen}
                  onOpenChange={setSettingsOpen}
                  quality={qualityGroup}
                  server={serverGroup}
                  subtitles={subtitleGroup}
                  speed={speedGroup}
                  audio={audioGroup}
                  onStats={() => setStatsOpen(true)}
                  buttonClassName={cn(ctrlBtn, settingsOpen && "bg-white/[0.12] text-white")}
                />
                <button
                  type="button"
                  className={ctrlBtn}
                  aria-label={fs ? "Exit fullscreen" : "Fullscreen"}
                  title={fs ? "Exit fullscreen (F)" : "Fullscreen (F)"}
                  onClick={(e) => toggleFullscreen(e)}
                >
                  {fs ? <Minimize size={21} /> : <Maximize size={21} />}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

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
        <PlayerErrorPanel
          message={err}
          details={errDetails}
          report={errReport}
          onRetry={() => {
            errShownRef.current = false;
            void createAndAttach("RETRY");
          }}
          onClose={onClose}
        />
      ) : null}
    </div>
  );
}
