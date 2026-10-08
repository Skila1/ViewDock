import { useCallback, useMemo, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { browseParams, genresOf, parseBrowse, toBrowseItems, type BrowseLayout, type BrowseQuery } from "@/lib/browse";

/** Every title the viewer can see (local and Jellyfin), plus their watch signals. */
export function useBrowseData() {
  const movies = useQuery({ queryKey: ["movies"], queryFn: api.listMovies });
  const series = useQuery({ queryKey: ["series"], queryFn: api.listSeries });
  const signals = useQuery({ queryKey: ["browse-signals"], queryFn: api.browseSignals, staleTime: 60_000 });
  const items = useMemo(() => toBrowseItems(movies.data ?? [], series.data ?? []), [movies.data, series.data]);
  const genres = useMemo(() => genresOf(items), [items]);
  return {
    items,
    genres,
    signals: signals.data,
    loading: movies.isLoading || series.isLoading,
    error: movies.error ?? series.error,
  };
}

/** The browse query lives in the home page URL, so it is shareable and survives reloads. */
export function useBrowseQuery(): [BrowseQuery, (patch: Partial<BrowseQuery>) => void] {
  const [params] = useSearchParams();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const onHome = pathname === "/";
  const query = useMemo(() => (onHome ? parseBrowse(params) : parseBrowse(new URLSearchParams())), [onHome, params]);
  const update = useCallback(
    (patch: Partial<BrowseQuery>) => {
      const next = browseParams({ ...query, ...patch }).toString();
      navigate(next ? `/?${next}` : "/", { replace: onHome });
    },
    [navigate, onHome, query],
  );
  return [query, update];
}

const LAYOUT_KEY = "vd.browse.layout";

export function useBrowseLayout(): [BrowseLayout, (layout: BrowseLayout) => void] {
  const [layout, setLayout] = useState<BrowseLayout>(() => {
    try {
      return localStorage.getItem(LAYOUT_KEY) === "list" ? "list" : "grid";
    } catch {
      return "grid";
    }
  });
  const set = useCallback((next: BrowseLayout) => {
    setLayout(next);
    try {
      localStorage.setItem(LAYOUT_KEY, next);
    } catch {
      /* private mode: the choice lasts for this page only */
    }
  }, []);
  return [layout, set];
}
