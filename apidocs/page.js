// Runs before the vendored Scalar bundle on the API reference page.
//
// CSP: Scalar adds <style> elements and style attributes at run time. The
// elements get this script's nonce and style attributes go through the
// CSSOM (which CSP does not restrict), so the page keeps the app's
// nonce-only style-src. Markup injected through innerHTML is not touched:
// its inline styles stay blocked, except the numeric line-number variables
// of highlighted code samples, re-applied from an allowlist.
//
// Accessibility: fixes the axe "serious" findings Scalar's markup has
// (scrollable endpoint list without keyboard access, "Show More" buttons
// whose accessible name does not contain their visible text).
(() => {
  const nonce = document.currentScript && document.currentScript.nonce;
  if (nonce) {
    const createElement = Document.prototype.createElement;
    Document.prototype.createElement = function (tag, options) {
      const el = createElement.call(this, tag, options);
      if (String(tag).toLowerCase() === "style") el.nonce = nonce;
      return el;
    };

    const setAttribute = Element.prototype.setAttribute;
    Element.prototype.setAttribute = function (name, value) {
      if (String(name).toLowerCase() === "style" && this.style) {
        this.style.cssText = String(value);
        return;
      }
      return setAttribute.call(this, name, value);
    };
  }

  const lineVars = /^\s*(?:--line-(?:count|digits):\s*\d{1,6};\s*)+$/;

  const fix = () => {
    for (const el of document.querySelectorAll(".endpoints:not([tabindex])")) {
      el.tabIndex = 0;
    }
    for (const b of document.querySelectorAll("button.show-more[aria-label]")) {
      const text = b.textContent.trim();
      const label = b.getAttribute("aria-label");
      if (text && !label.toLowerCase().startsWith(text.toLowerCase())) {
        b.setAttribute("aria-label", text + ": " + label);
      }
    }
    for (const el of document.querySelectorAll("code[style]")) {
      const css = el.getAttribute("style");
      if (el.style.length === 0 && lineVars.test(css)) {
        for (const [, prop, val] of css.matchAll(/(--line-(?:count|digits)):\s*(\d+)/g)) {
          el.style.setProperty(prop, val);
        }
      }
    }
  };

  let queued = false;
  new MutationObserver(() => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => {
      queued = false;
      fix();
    });
  }).observe(document.documentElement, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["aria-label"],
  });
})();
