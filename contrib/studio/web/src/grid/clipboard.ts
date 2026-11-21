/**
 * copyText puts text on the clipboard. Studio is served from 127.0.0.1, a
 * secure context, so the async Clipboard API is there; it still fails when the
 * browser denies it, and then the caller shows the text to select by hand.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}
