export type NowPlayingInfo = {
  /** Movie or series name. */
  title: string;
  /** For episodes, for example "Season 1". */
  season?: string;
  /** For episodes, for example "Strike First: Ep. 2". */
  episode?: string;
  /** Short facts such as year and rating. */
  meta?: string[];
  overview?: string;
};

type Props = {
  info: NowPlayingInfo;
  visible: boolean;
};

export function PauseOverlay({ info, visible }: Props) {
  return (
    <div
      aria-hidden={!visible}
      className={
        "pointer-events-none absolute inset-0 z-[5] transition-opacity duration-700 ease-out " + (visible ? "opacity-100" : "opacity-0")
      }
    >
      <div className="absolute inset-0 bg-black/45" />
      <div className="absolute inset-0 bg-gradient-to-r from-black/85 via-black/45 to-transparent" />
      <div
        className={
          "absolute left-[6vw] top-1/2 w-[min(40rem,80vw)] -translate-y-1/2 transition-transform duration-700 ease-out " +
          (visible ? "translate-x-0" : "-translate-x-4")
        }
      >
        <p className="text-[clamp(0.8rem,1.1vw,1rem)] font-medium tracking-wide text-white/65">You&apos;re watching</p>
        <h2 className="mt-2 text-[clamp(1.9rem,4.4vw,3.6rem)] font-bold leading-[1.05] tracking-tight text-white [text-wrap:balance]">
          {info.title}
        </h2>
        {info.season ? <p className="mt-3 text-[clamp(1rem,1.5vw,1.3rem)] font-semibold text-white">{info.season}</p> : null}
        {info.episode ? (
          <p className="mt-[clamp(0.9rem,2vw,1.6rem)] text-[clamp(1rem,1.5vw,1.3rem)] font-semibold text-white">{info.episode}</p>
        ) : null}
        {info.meta?.length ? (
          <p className="mt-3 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[clamp(0.85rem,1.15vw,1rem)] font-medium text-white/70">
            {info.meta.map((m, i) => (
              <span key={m} className="flex items-center gap-2.5">
                {i > 0 ? <span className="h-1 w-1 rounded-full bg-white/40" aria-hidden /> : null}
                {m}
              </span>
            ))}
          </p>
        ) : null}
        {info.overview ? (
          <p className="mt-3 line-clamp-4 max-w-[36rem] text-[clamp(0.85rem,1.15vw,1.05rem)] leading-relaxed text-white/80">
            {info.overview}
          </p>
        ) : null}
      </div>
      <p className="absolute bottom-[max(2.25rem,var(--sab))] right-[4vw] text-[clamp(0.85rem,1.1vw,1rem)] font-medium tracking-wide text-white/70">
        Paused
      </p>
    </div>
  );
}
