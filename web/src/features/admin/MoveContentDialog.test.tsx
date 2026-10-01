import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Library, MoveJob, MovePlan } from "@/types/api.gen";

vi.mock("@/api/api", () => ({
  api: {
    previewMove: vi.fn(),
    startMove: vi.fn(),
    getMoveJob: vi.fn(),
  },
}));

import { api } from "@/api/api";
import { MoveContentDialog } from "./MoveContentDialog";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const lib = (id: string, content_type: Library["content_type"], name: string): Library => ({ id, name, path: `/media/${name}`, content_type, uploads_enabled: true });
const mixed = lib("x", "mixed", "Mixed");
const movies = lib("m", "movies", "Movies");
const shows = lib("t", "tv", "Shows");

const plan: MovePlan = {
  destination: movies,
  eligible: [
    { kind: "movie", id: "a", title: "Heat", year: 1995, files: 1, bytes: 1024, target: "Heat (1995)" },
    { kind: "movie", id: "b", title: "Alien", files: 1, bytes: 1024, target: "Alien.mkv", renamed: true },
  ],
  ineligible: [{ kind: "series", id: "s", title: "Lost", files: 3, bytes: 0, reason: "incompatible", message: "TV shows cannot go into Movies." }],
  eligible_bytes: 2048,
};

let root: Root;
let host: HTMLDivElement;

async function render(ui: React.ReactNode) {
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient();
  await act(async () => root.render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>));
}

async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

beforeEach(() => {
  vi.mocked(api.previewMove).mockResolvedValue(plan);
});

afterEach(() => {
  act(() => root.unmount());
  vi.clearAllMocks();
});

describe("MoveContentDialog", () => {
  it("offers only compatible destinations and previews eligible and skipped titles", async () => {
    await render(<MoveContentDialog open onOpenChange={() => undefined} libraries={[mixed, movies, shows]} source={mixed} />);
    const select = document.querySelector<HTMLSelectElement>('select[aria-label="Destination library"]')!;
    const options = Array.from(select.options).filter((o) => o.value);
    expect(options.map((o) => o.value)).toEqual(["m", "t"]); // the source itself is not offered
    expect(options.every((o) => !o.disabled)).toBe(true);

    await act(async () => {
      select.value = "m";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flush();
    expect(api.previewMove).toHaveBeenCalledWith({ source_library_id: "x", destination_library_id: "m", all: true });
    const text = document.body.textContent ?? "";
    expect(text).toContain("2 titles will move");
    expect(text).toContain("1 will be skipped");
    expect(text).toContain("Skipped: TV shows cannot go into this library");
    expect(text).toContain("Only movies can move here");
    const confirm = Array.from(document.querySelectorAll("button")).find((b) => b.textContent === "Move 2 titles");
    expect(confirm?.disabled).toBe(false);
  });

  it("marks libraries that cannot hold the selection as incompatible", async () => {
    await render(
      <MoveContentDialog open onOpenChange={() => undefined} libraries={[mixed, movies, shows]} items={[{ kind: "series", id: "s", libraryId: "x" }]} />,
    );
    const select = document.querySelector<HTMLSelectElement>('select[aria-label="Destination library"]')!;
    const movieOpt = Array.from(select.options).find((o) => o.value === "m")!;
    expect(movieOpt.disabled).toBe(true);
    expect(movieOpt.textContent).toContain("incompatible");
    expect(Array.from(select.options).find((o) => o.value === "t")?.disabled).toBe(false);
  });

  it("starts the move and reports the result", async () => {
    const done: MoveJob = {
      id: "j",
      destination_library_id: "m",
      status: "done",
      total: 3,
      moved: 2,
      skipped: 1,
      failed: 0,
      created_at: "",
      items: [],
    };
    vi.mocked(api.startMove).mockResolvedValue(done);
    const onDone = vi.fn();
    await render(<MoveContentDialog open onOpenChange={() => undefined} libraries={[mixed, movies]} source={mixed} onDone={onDone} />);
    const select = document.querySelector<HTMLSelectElement>('select[aria-label="Destination library"]')!;
    await act(async () => {
      select.value = "m";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flush();
    const confirm = Array.from(document.querySelectorAll("button")).find((b) => b.textContent === "Move 2 titles")!;
    await act(async () => confirm.click());
    await flush();
    expect(api.startMove).toHaveBeenCalledWith({ source_library_id: "x", destination_library_id: "m", all: true });
    expect(document.body.textContent).toContain("2 moved, 1 skipped");
    expect(onDone).toHaveBeenCalled();
  });

  it("disables moving when nothing is eligible", async () => {
    vi.mocked(api.previewMove).mockResolvedValue({ ...plan, eligible: [], eligible_bytes: 0 });
    await render(<MoveContentDialog open onOpenChange={() => undefined} libraries={[mixed, movies]} source={mixed} />);
    const select = document.querySelector<HTMLSelectElement>('select[aria-label="Destination library"]')!;
    await act(async () => {
      select.value = "m";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flush();
    expect(document.body.textContent).toContain("Nothing can move");
    const confirm = Array.from(document.querySelectorAll("button")).find((b) => b.textContent === "Move")!;
    expect(confirm.disabled).toBe(true);
  });
});
