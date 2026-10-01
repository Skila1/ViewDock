import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { WatchTogetherOverlay } from "./WatchTogetherOverlay";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const sync = { playing: false, position: 0 } as never;

describe("WatchTogetherOverlay", () => {
  it("offers Leave party only when the page can leave", () => {
    const host = document.createElement("div");
    document.body.appendChild(host);
    const root = createRoot(host);
    const onLeave = vi.fn();
    act(() => root.render(<WatchTogetherOverlay members={[]} sync={sync} status="synced" onLeave={onLeave} />));
    const leave = Array.from(host.querySelectorAll("button")).find((b) => b.textContent?.includes("Leave party"));
    expect(leave).toBeTruthy();
    act(() => leave!.click());
    expect(onLeave).toHaveBeenCalledTimes(1);
    act(() => root.render(<WatchTogetherOverlay members={[]} sync={sync} status="synced" />));
    expect(host.textContent).not.toContain("Leave party");
    act(() => root.unmount());
  });
});
