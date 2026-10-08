/**
 * Copies text synchronously through a selection first, because iOS only
 * allows copying inside the user gesture, then falls back to the async
 * Clipboard API on secure origins.
 */
export async function copyText(text: string, source?: HTMLElement | null): Promise<boolean> {
  if (copyBySelection(text, source ?? null)) return true;
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      return false;
    }
  }
  return false;
}

export function copyBySelection(text: string, source: HTMLElement | null): boolean {
  try {
    if (source) {
      const sel = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(source);
      sel?.removeAllRanges();
      sel?.addRange(range);
      if (document.execCommand("copy")) {
        sel?.removeAllRanges();
        return true;
      }
      sel?.removeAllRanges();
    }
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.cssText = "position:fixed;top:0;left:0;width:2em;height:2em;opacity:0.01;border:none;padding:0";
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, text.length);
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
