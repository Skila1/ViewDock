import type { ReactNode } from "react";
import * as Menu from "@radix-ui/react-dropdown-menu";
import { ArrowUpDown, Check, ChevronDown, ListFilter } from "lucide-react";
import { cn } from "@/lib/cn";
import { KIND_OPTIONS, SORT_OPTIONS, TAG_OPTIONS, type BrowseQuery } from "@/lib/browse";

type FilterProps = {
  query: BrowseQuery;
  genres: string[];
  onChange: (patch: Partial<BrowseQuery>) => void;
};

const trigger =
  "flex h-9 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-3 text-[13px] text-dim outline-none transition-colors hover:bg-overlay hover:text-ink focus-visible:ring-2 focus-visible:ring-accent data-[state=open]:bg-overlay data-[state=open]:text-ink";
const content = "z-50 min-w-[12rem] rounded-xl border border-line bg-raised p-1 shadow-xl";
const item =
  "flex cursor-pointer select-none items-center gap-2 rounded-lg px-2.5 py-2 text-[13px] text-dim outline-none data-[highlighted]:bg-overlay data-[highlighted]:text-ink data-[state=checked]:text-ink";
/** Scrollbars are hidden site-wide, so long lists fade at the bottom to show there is more. */
const scrollFade = "pb-6 [mask-image:linear-gradient(to_bottom,black_calc(100%-2.5rem),transparent)]";
const menuLabel = "px-2.5 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wide text-dim";

function ItemCheck() {
  return (
    <span className="flex w-4 shrink-0 justify-center">
      <Menu.ItemIndicator>
        <Check className="h-3.5 w-3.5 text-accent" />
      </Menu.ItemIndicator>
    </span>
  );
}

function MenuContent({ children, align = "start", long }: { children: ReactNode; align?: "start" | "end"; long?: boolean }) {
  return (
    <Menu.Portal>
      <Menu.Content align={align} sideOffset={6} collisionPadding={12} className={content}>
        <div className={cn("max-h-[min(22rem,calc(var(--radix-dropdown-menu-content-available-height)-0.5rem))] overflow-y-auto", long && scrollFade)}>
          {children}
        </div>
      </Menu.Content>
    </Menu.Portal>
  );
}

/** All | Movies | TV shows | Anime as one segmented control. */
export function KindSegment({ query, onChange, className }: Pick<FilterProps, "query" | "onChange"> & { className?: string }) {
  return (
    <div role="radiogroup" aria-label="Media type" className={cn("flex shrink-0 rounded-lg bg-bg/60 p-0.5", className)}>
      {KIND_OPTIONS.map((o) => {
        const on = query.kind === o.value;
        return (
          <button
            key={o.value || "all"}
            type="button"
            role="radio"
            aria-checked={on}
            onClick={() => onChange({ kind: o.value })}
            className={cn(
              "h-8 flex-1 whitespace-nowrap rounded-md px-3 text-[13px] transition-colors",
              on ? "bg-overlay font-medium text-ink shadow-sm" : "text-dim hover:text-ink",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function GenreMenu({ query, genres, onChange }: FilterProps) {
  const options = query.genre && !genres.includes(query.genre) ? [query.genre, ...genres] : genres;
  return (
    <Menu.Root modal={false}>
      <Menu.Trigger className={cn(trigger, query.genre && "text-ink")} disabled={!options.length}>
        {query.genre || (genres.length ? "All genres" : "No genres yet")}
        <ChevronDown className="h-3.5 w-3.5 opacity-70" />
      </Menu.Trigger>
      <MenuContent long={options.length > 12}>
        <Menu.RadioGroup value={query.genre} onValueChange={(genre) => onChange({ genre })}>
          <Menu.RadioItem value="" className={item}>
            <ItemCheck />
            All genres
          </Menu.RadioItem>
          {options.map((g) => (
            <Menu.RadioItem key={g} value={g} className={item}>
              <ItemCheck />
              {g}
            </Menu.RadioItem>
          ))}
        </Menu.RadioGroup>
      </MenuContent>
    </Menu.Root>
  );
}

function TagMenu({ query, onChange }: Pick<FilterProps, "query" | "onChange">) {
  const active = TAG_OPTIONS.find((o) => o.value === query.tag);
  return (
    <Menu.Root modal={false}>
      <Menu.Trigger className={cn(trigger, active && "text-ink")}>
        <ListFilter className="h-3.5 w-3.5" />
        {active ? active.label : "Filters"}
        {active ? <span className="h-1.5 w-1.5 rounded-full bg-accent" /> : null}
      </Menu.Trigger>
      <MenuContent>
        <Menu.Label className={menuLabel}>Show</Menu.Label>
        {TAG_OPTIONS.map((o) => (
          <Menu.CheckboxItem
            key={o.value}
            checked={query.tag === o.value}
            onCheckedChange={(on) => onChange({ tag: on ? o.value : "" })}
            className={item}
          >
            <ItemCheck />
            {o.label}
          </Menu.CheckboxItem>
        ))}
      </MenuContent>
    </Menu.Root>
  );
}

function SortMenu({ query, onChange }: Pick<FilterProps, "query" | "onChange">) {
  const label = SORT_OPTIONS.find((o) => o.value === query.sort)?.label ?? SORT_OPTIONS[0].label;
  return (
    <Menu.Root modal={false}>
      <Menu.Trigger className={cn(trigger, query.sort && "text-ink")} aria-label={`Sort: ${label}`}>
        <ArrowUpDown className="h-3.5 w-3.5" />
        {label}
      </Menu.Trigger>
      <MenuContent align="end">
        <Menu.Label className={menuLabel}>Sort by</Menu.Label>
        <Menu.RadioGroup value={query.sort} onValueChange={(v) => onChange({ sort: v as BrowseQuery["sort"] })}>
          {SORT_OPTIONS.map((o) => (
            <Menu.RadioItem key={o.value || "best"} value={o.value} className={item}>
              <ItemCheck />
              {o.label}
            </Menu.RadioItem>
          ))}
        </Menu.RadioGroup>
      </MenuContent>
    </Menu.Root>
  );
}

function Divider({ className }: { className?: string }) {
  return <span aria-hidden className={cn("mx-1 h-5 w-px shrink-0 bg-line", className)} />;
}

/**
 * The home page toolbar: one bar holding media type, genre, tags and sort,
 * with optional trailing controls such as the layout toggle.
 */
export function BrowseToolbar({ query, genres, onChange, trailing, className }: FilterProps & { trailing?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-wrap items-center gap-1 rounded-xl border border-line bg-raised/50 p-1 sm:flex-nowrap sm:overflow-x-auto", className)}>
      <KindSegment query={query} onChange={onChange} className="w-full sm:w-auto" />
      <Divider className="hidden sm:block" />
      <GenreMenu query={query} genres={genres} onChange={onChange} />
      <TagMenu query={query} onChange={onChange} />
      <div className="ml-auto flex shrink-0 items-center gap-1">
        <SortMenu query={query} onChange={onChange} />
        {trailing ? (
          <>
            <Divider />
            {trailing}
          </>
        ) : null}
      </div>
    </div>
  );
}

function PanelOption({ on, onClick, children }: { on: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[13px] transition-colors",
        on ? "bg-overlay text-ink" : "text-dim hover:bg-overlay/60 hover:text-ink",
      )}
    >
      <span className="flex w-4 shrink-0 justify-center">{on ? <Check className="h-3.5 w-3.5 text-accent" /> : null}</span>
      <span className="truncate">{children}</span>
    </button>
  );
}

const panelLabel = "px-2.5 pb-1 text-[11px] font-semibold uppercase tracking-wide text-dim";

/** The header search dropdown: the same filters laid out as a menu panel. */
export function BrowseFilterPanel({ query, genres, onChange, onReset }: FilterProps & { onReset: () => void }) {
  const options = query.genre && !genres.includes(query.genre) ? [query.genre, ...genres] : genres;
  const active = Boolean(query.kind || query.genre || query.tag || query.sort);
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between px-1">
        <p className="text-sm font-medium text-ink">Filter results</p>
        {active ? (
          <button type="button" className="text-xs text-accent hover:underline" onClick={onReset}>
            Reset
          </button>
        ) : null}
      </div>
      <KindSegment query={query} onChange={onChange} className="w-full" />
      <div className="grid grid-cols-2 gap-3">
        <section aria-label="Genre" className="min-w-0">
          <p className={panelLabel}>Genre</p>
          <div className={cn("max-h-64 space-y-0.5 overflow-y-auto", options.length > 8 && scrollFade)}>
            <PanelOption on={!query.genre} onClick={() => onChange({ genre: "" })}>
              All genres
            </PanelOption>
            {options.map((g) => (
              <PanelOption key={g} on={query.genre === g} onClick={() => onChange({ genre: g })}>
                {g}
              </PanelOption>
            ))}
            {!options.length ? <p className="px-2.5 py-1.5 text-xs text-dim">No genres yet</p> : null}
          </div>
        </section>
        <div className="min-w-0 space-y-3">
          <section aria-label="Show">
            <p className={panelLabel}>Show</p>
            <div className="space-y-0.5">
              {TAG_OPTIONS.map((o) => (
                <PanelOption key={o.value} on={query.tag === o.value} onClick={() => onChange({ tag: query.tag === o.value ? "" : o.value })}>
                  {o.label}
                </PanelOption>
              ))}
            </div>
          </section>
          <section aria-label="Sort by">
            <p className={panelLabel}>Sort by</p>
            <div className="space-y-0.5">
              {SORT_OPTIONS.map((o) => (
                <PanelOption key={o.value || "best"} on={query.sort === o.value} onClick={() => onChange({ sort: o.value })}>
                  {o.label}
                </PanelOption>
              ))}
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
