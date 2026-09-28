import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { Captions, Check, ChevronLeft, ChevronRight, Gauge, Server, Settings, SlidersHorizontal, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/cn";

export type MenuOption<T> = { value: T; label: string; hint?: string };

export type MenuGroup<T> = {
  value: T;
  options: MenuOption<T>[];
  onChange: (value: T) => void;
  /** Shown in place of the options, for example when none exist. */
  empty?: string;
  /** Disables the group with this explanation. */
  disabled?: string;
};

type View = "root" | "quality" | "server" | "subtitles" | "speed";

/** Any group, with its value type erased for the shared row and option rendering. */
type ErasedGroup = {
  value: unknown;
  options: MenuOption<unknown>[];
  onChange: (value: never) => void;
  empty?: string;
  disabled?: string;
};

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  quality: MenuGroup<string> | null;
  server: MenuGroup<string> | null;
  subtitles: MenuGroup<number | null>;
  speed: MenuGroup<number>;
  buttonClassName: string;
};

const TITLES: Record<Exclude<View, "root">, string> = {
  quality: "Quality",
  server: "Server",
  subtitles: "Subtitles",
  speed: "Playback speed",
};

function currentLabel(g: ErasedGroup): string {
  if (g.disabled) return g.disabled;
  return g.options.find((o) => o.value === g.value)?.label ?? (g.options.length ? "" : (g.empty ?? ""));
}

function focusItems(root: HTMLElement | null): HTMLButtonElement[] {
  return root ? Array.from(root.querySelectorAll<HTMLButtonElement>("[data-menu-item]:not([disabled])")) : [];
}

export function PlayerSettingsMenu({ open, onOpenChange, quality, server, subtitles, speed, buttonClassName }: Props) {
  const [view, setView] = useState<View>("root");
  const wrapRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) {
      setView("root");
      return;
    }
    const onDown = (e: PointerEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) onOpenChange(false);
    };
    document.addEventListener("pointerdown", onDown, true);
    return () => document.removeEventListener("pointerdown", onDown, true);
  }, [open, onOpenChange]);

  useEffect(() => {
    if (!open) return;
    const items = focusItems(panelRef.current);
    const checked = items.find((el) => el.getAttribute("aria-checked") === "true");
    (checked ?? items[0])?.focus({ preventScroll: true });
  }, [open, view]);

  const close = (refocus: boolean) => {
    onOpenChange(false);
    if (refocus) buttonRef.current?.focus();
  };

  const onKeyDown = (e: ReactKeyboardEvent) => {
    const items = focusItems(panelRef.current);
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const next = e.key === "ArrowDown" ? (at + 1) % items.length : (at - 1 + items.length) % items.length;
      items[next]?.focus();
    } else if (e.key === "Escape") {
      e.preventDefault();
      if (view === "root") close(true);
      else setView("root");
    } else if ((e.key === "ArrowLeft" || e.key === "Backspace") && view !== "root") {
      e.preventDefault();
      setView("root");
    }
    e.stopPropagation();
  };

  const rows: { view: Exclude<View, "root">; icon: LucideIcon; group: ErasedGroup | null }[] = [
    { view: "quality", icon: SlidersHorizontal, group: quality },
    { view: "server", icon: Server, group: server },
    { view: "subtitles", icon: Captions, group: subtitles },
    { view: "speed", icon: Gauge, group: speed },
  ];

  const pick = (g: ErasedGroup, value: unknown) => {
    if (value !== g.value) (g.onChange as (v: unknown) => void)(value);
    close(true);
  };

  let body: ReactNode;
  if (view === "root") {
    body = (
      <div className="py-1.5">
        {rows.map(({ view: v, icon: Icon, group }) =>
          group ? (
            <button
              key={v}
              type="button"
              role="menuitem"
              data-menu-item
              aria-haspopup="menu"
              disabled={Boolean(group.disabled)}
              onClick={() => setView(v)}
              className="group/row flex w-full items-center gap-3 px-4 py-2.5 text-left text-[14px] text-white/90 outline-none transition-colors hover:bg-white/[0.07] focus-visible:bg-white/[0.09] disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Icon size={18} strokeWidth={1.8} className="shrink-0 text-white/70" aria-hidden />
              <span className="flex-1 font-medium">{TITLES[v]}</span>
              <span className="max-w-[8.5rem] truncate text-[13px] text-white/50">{currentLabel(group)}</span>
              <ChevronRight size={16} className="shrink-0 text-white/35 transition-transform group-hover/row:translate-x-0.5" aria-hidden />
            </button>
          ) : null,
        )}
      </div>
    );
  } else {
    const group = rows.find((r) => r.view === view)?.group ?? null;
    body = (
      <>
        <button
          type="button"
          data-menu-item
          onClick={() => setView("root")}
          className="flex w-full items-center gap-2 border-b border-white/[0.08] px-3 py-3 text-left text-[14px] font-semibold text-white outline-none transition-colors hover:bg-white/[0.05] focus-visible:bg-white/[0.08]"
        >
          <ChevronLeft size={18} className="text-white/70" aria-hidden />
          {TITLES[view]}
        </button>
        <div className="max-h-[min(18rem,50vh)] overflow-y-auto py-1.5" role="group" aria-label={TITLES[view]}>
          {group && group.options.length === 0 ? (
            <p className="px-4 py-3 text-[13px] text-white/50">{group.empty ?? "Nothing to choose from."}</p>
          ) : null}
          {group?.options.map((o) => {
            const selected = o.value === group.value;
            return (
              <button
                key={String(o.value)}
                type="button"
                role="menuitemradio"
                aria-checked={selected}
                data-menu-item
                onClick={() => pick(group, o.value)}
                className={cn(
                  "flex w-full items-center gap-3 px-4 py-2.5 text-left text-[14px] outline-none transition-colors hover:bg-white/[0.07] focus-visible:bg-white/[0.09]",
                  selected ? "text-white" : "text-white/75",
                )}
              >
                <span className="grid w-[18px] shrink-0 place-items-center">
                  {selected ? <Check size={17} strokeWidth={2.4} className="text-accent" aria-hidden /> : null}
                </span>
                <span className={cn("flex-1", selected && "font-semibold")}>{o.label}</span>
                {o.hint ? <span className="text-[12px] text-white/40">{o.hint}</span> : null}
              </button>
            );
          })}
        </div>
      </>
    );
  }

  return (
    <div ref={wrapRef} className="relative">
      <button
        ref={buttonRef}
        type="button"
        className={buttonClassName}
        aria-label="Settings"
        aria-haspopup="menu"
        aria-expanded={open}
        title="Settings"
        onClick={() => onOpenChange(!open)}
      >
        <Settings size={21} strokeWidth={1.9} className={cn("transition-transform duration-300 ease-out", open && "rotate-[60deg]")} aria-hidden />
      </button>
      {open ? (
        <div
          ref={panelRef}
          role="menu"
          aria-label="Player settings"
          onKeyDown={onKeyDown}
          onClick={(e) => e.stopPropagation()}
          className="vd-menu-pop absolute bottom-full right-0 z-30 mb-3 w-[19rem] max-w-[calc(100vw-1.5rem)] origin-bottom-right overflow-hidden rounded-2xl border border-white/10 bg-[#121214]/95 shadow-[0_28px_70px_-16px_rgba(0,0,0,0.85)] ring-1 ring-black/40 backdrop-blur-xl"
        >
          <div key={view} className={view === "root" ? "vd-menu-back" : "vd-menu-forward"}>
            {body}
          </div>
        </div>
      ) : null}
    </div>
  );
}
