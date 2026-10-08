import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/api/api", async () => {
  const actual = await vi.importActual<typeof import("@/api/api")>("@/api/api");
  return {
    ...actual,
    api: {
      activityRoom: vi.fn(),
      listMovies: vi.fn(),
      listSeries: vi.fn(),
      search: vi.fn(),
      getSeries: vi.fn(),
    },
  };
});
vi.mock("@/lib/discordActivity", async () => {
  const actual = await vi.importActual<typeof import("@/lib/discordActivity")>("@/lib/discordActivity");
  return { ...actual, activityInstanceId: () => "i-1" };
});

import { api } from "@/api/api";
import { leftPartyCode, rememberLeftParty } from "@/lib/discordActivity";
import { ActivityPage } from "./ActivityPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let where = "";

function Where() {
  const loc = useLocation();
  where = loc.pathname;
  return <p>at {loc.pathname}</p>;
}

async function render() {
  const host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () =>
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/activity"]}>
          <Routes>
            <Route path="/activity" element={<ActivityPage />} />
            <Route path="*" element={<Where />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  );
  for (let i = 0; i < 3; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

const text = () => document.body.textContent ?? "";
const button = (label: string) => Array.from(document.querySelectorAll("button")).find((b) => b.textContent?.includes(label));

beforeEach(() => {
  where = "";
  window.sessionStorage.clear();
  vi.mocked(api.listMovies).mockResolvedValue([
    { id: "m1", title: "Heat", year: 1995, poster_url: null, unmatched: false, metadata_source: "tmdb" },
  ]);
  vi.mocked(api.listSeries).mockResolvedValue([]);
});

afterEach(() => {
  act(() => root.unmount());
  vi.clearAllMocks();
});

describe("ActivityPage", () => {
  it("lets the channel host pick the title", async () => {
    vi.mocked(api.activityRoom).mockResolvedValue({ room: null, can_create: true, host: { name: "Skila", you: true } });
    await render();
    expect(text()).toContain("You're hosting");
    expect(text()).toContain("Heat");
  });

  it("finds titles when the host searches", async () => {
    vi.mocked(api.activityRoom).mockResolvedValue({ room: null, can_create: true, host: { name: "Skila", you: true } });
    await render();
    const input = document.querySelector<HTMLInputElement>('input[type="search"]')!;
    const type = async (value: string) => {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
        input.dispatchEvent(new Event("input", { bubbles: true }));
        await new Promise((r) => setTimeout(r, 350));
      });
    };
    await type("hea");
    expect(text()).toContain("Heat");
    expect(text()).not.toContain("No matches");
    await type("zzz");
    expect(text()).toContain("No matches");
    expect(api.search).not.toHaveBeenCalled();
  });

  it("makes everyone else wait for the host", async () => {
    vi.mocked(api.activityRoom).mockResolvedValue({ room: null, can_create: false, host: { name: "Skila", you: false } });
    await render();
    expect(text()).toContain("Skila is hosting this channel");
    expect(text()).not.toContain("Heat");
  });

  it("sends everyone into the channel's party", async () => {
    vi.mocked(api.activityRoom).mockResolvedValue({ room: { room_id: "r", invite_code: "CODE1", title: "Heat" }, can_create: false });
    await render();
    expect(where).toBe("/together/CODE1");
  });

  it("after leaving, offers Rejoin and lets the viewer watch on their own", async () => {
    rememberLeftParty("CODE1");
    vi.mocked(api.activityRoom).mockResolvedValue({ room: { room_id: "r", invite_code: "CODE1", title: "Heat" }, can_create: false });
    await render();
    expect(where).toBe("");
    expect(text()).toContain("The channel's party is still playing");
    expect(text()).toContain("You left the party");
    // Picking a title plays it alone, not in the party.
    const poster = Array.from(document.querySelectorAll("button")).find((b) => b.textContent?.includes("Heat") && !b.textContent.includes("Rejoin"))!;
    await act(async () => poster.click());
    expect(where).toBe("/watch/movie/m1");
    expect(api.activityRoom).toHaveBeenCalledTimes(1); // no party was created or joined
  });

  it("Rejoin goes back into the party and forgets leaving", async () => {
    rememberLeftParty("CODE1");
    vi.mocked(api.activityRoom).mockResolvedValue({ room: { room_id: "r", invite_code: "CODE1", title: "Heat" }, can_create: false });
    await render();
    await act(async () => button("Rejoin")!.click());
    expect(where).toBe("/together/CODE1");
    expect(leftPartyCode()).toBe("");
  });

  it("joins a new party when the one left has ended", async () => {
    rememberLeftParty("OLD");
    vi.mocked(api.activityRoom).mockResolvedValue({ room: { room_id: "r2", invite_code: "NEW", title: "Up" }, can_create: false });
    await render();
    expect(where).toBe("/together/NEW");
  });
});
