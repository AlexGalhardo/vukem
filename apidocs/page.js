// Runs before the vendored Scalar bundle on the API reference page.
//
// CSP: Scalar adds <style> elements and style attributes at run time. The
// elements it creates with createElement get this script's nonce, and
// style attributes it sets with setAttribute go through the CSSOM (which
// CSP does not restrict) when every declaration is a plain size or
// overflow value (Scalar's scrollbar probe); anything else is left to CSP,
// which blocks it. The page keeps the app's nonce-only style-src.
//
// Text from the OpenAPI document (descriptions rendered as HTML) reaches
// the DOM through innerHTML, never through createElement or setAttribute,
// so its <style> elements and style attributes stay blocked. The one
// exception is the numeric line-number variables Scalar puts on highlighted
// code samples (style="--line-count: N; --line-digits: N;"): the innerHTML
// setter moves an attribute that is exactly that to a data attribute before
// parsing (so CSP never sees it) and applies it through the CSSOM.
//
// Zod (bundled in Scalar) probes for eval with Function("") unless its
// global config is jitless; the probe is a script-src violation, so the
// config is set first.
//
// Accessibility: fixes the findings Scalar's markup has (a scrollable
// endpoint list without keyboard access, "Show More" buttons whose
// accessible name does not contain their visible text, intro-card captions
// marked up as a <label> that labels nothing, and the combobox filter field
// of a dropdown that passes no label).
(() => {
  const sizeProps = /^(?:(?:min-|max-)?(?:width|height)|overflow(?:-x|-y)?)$/;
  const plainValue = /^[a-z0-9.%\s-]+$/i;
  const safeStyle = (css) =>
    css.split(";").every((decl) => {
      if (decl.trim() === "") return true;
      const i = decl.indexOf(":");
      return i > 0 && sizeProps.test(decl.slice(0, i).trim().toLowerCase()) && plainValue.test(decl.slice(i + 1));
    });

  const lineVars = /^\s*(?:--line-(?:count|digits):\s*\d{1,6};\s*)+$/;
  const lineVarsAttr = /\sstyle="([^"]*)"/g;
  const lineVarsData = "data-vukem-line-vars";

  const nonce = document.currentScript && document.currentScript.nonce;
  if (nonce) {
    globalThis.__zod_globalConfig = Object.assign(globalThis.__zod_globalConfig || {}, { jitless: true });

    const createElement = Document.prototype.createElement;
    Document.prototype.createElement = function (tag, options) {
      const el = createElement.call(this, tag, options);
      if (String(tag).toLowerCase() === "style") el.nonce = nonce;
      return el;
    };

    const setAttribute = Element.prototype.setAttribute;
    Element.prototype.setAttribute = function (name, value) {
      if (String(name).toLowerCase() === "style" && this.style && safeStyle(String(value))) {
        this.style.cssText = String(value);
        return;
      }
      return setAttribute.call(this, name, value);
    };

    const innerHTML = Object.getOwnPropertyDescriptor(Element.prototype, "innerHTML");
    if (innerHTML && innerHTML.set) {
      Object.defineProperty(Element.prototype, "innerHTML", {
        configurable: true,
        enumerable: innerHTML.enumerable,
        get: innerHTML.get,
        set(value) {
          // Vue hands static markup over as TrustedHTML; the docs CSP does
          // not enforce Trusted Types, so the rewritten string is accepted.
          const html =
            typeof value === "string" || (typeof TrustedHTML === "function" && value instanceof TrustedHTML)
              ? String(value)
              : "";
          if (!html.includes("--line-")) {
            innerHTML.set.call(this, value);
            return;
          }
          const moved = html.replace(lineVarsAttr, (attr, css) =>
            lineVars.test(css) ? ` ${lineVarsData}="${css}"` : attr,
          );
          if (moved === html) {
            innerHTML.set.call(this, value);
            return;
          }
          innerHTML.set.call(this, moved);
          // Vue parses static markup in a <template>, whose children live in .content.
          const root = this.tagName === "TEMPLATE" && this.content ? this.content : this;
          for (const el of root.querySelectorAll(`[${lineVarsData}]`)) {
            const css = el.getAttribute(lineVarsData);
            el.removeAttribute(lineVarsData);
            if (!lineVars.test(css)) continue;
            for (const [, prop, val] of css.matchAll(/(--line-(?:count|digits)):\s*(\d+)/g)) {
              el.style.setProperty(prop, val);
            }
          }
        },
      });
    }
  }

  // An element has an accessible name from its own markup. Scalar's
  // combobox field is an <input>, so a placeholder names it too.
  const named = (el) =>
    ["aria-label", "aria-labelledby", "title", "placeholder"].some((a) => (el.getAttribute(a) || "").trim() !== "") ||
    (el.labels && el.labels.length > 0);

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
    // Scalar's intro cards caption their value with a <label> that has no
    // "for" and wraps no control (ServerSelector renders "Server" that way),
    // which is what DevTools reports as "No label associated with a form
    // field". A caption is a <span>, so retag it and keep everything else.
    // The caption is a hoisted static node Vue never patches; if a Scalar
    // version re-inserts the original, the observer below retags it again.
    for (const label of document.querySelectorAll("label:not([for])")) {
      if (label.control) continue;

      const span = document.createElement("span");
      for (const name of label.getAttributeNames()) {
        // A style attribute goes through the CSSOM: setAttribute would hand
        // CSP an inline style the page's policy blocks.
        if (name !== "style") span.setAttribute(name, label.getAttribute(name));
      }
      if (label.style.cssText) span.style.cssText = label.style.cssText;
      span.append(...label.childNodes);
      label.replaceWith(span);
    }
    // Scalar names its combobox filter field only when the component that
    // opens it passes inputLabel: the client picker and the search modal do,
    // the auth-scheme selector does not, leaving a role="combobox" input
    // with no accessible name. The page is served as lang="en", so the
    // fallback is English like the rest of the shim.
    for (const el of document.querySelectorAll('input[role="combobox"]')) {
      if (!named(el)) el.setAttribute("aria-label", "Search");
    }
    // A marker, so a test can tell "the pass ran" from "the page never
    // rendered". Without it the only signal is elapsed time, and a slow
    // runner then fails a page that was simply not finished.
    document.documentElement.setAttribute("data-vukem-apidocs", "fixed");
  };

  let queued = false;
  new MutationObserver(() => {
    if (queued) return;
    queued = true;
    // A microtask, not requestAnimationFrame: rAF needs a frame to be
    // painted, and a headless dump under a virtual-time budget can end
    // before one is. The pass then never ran and the page kept the markup
    // this shim exists to fix — which is exactly how this failed on a slow
    // Windows runner. A microtask still coalesces a burst of mutations into
    // one pass, and always runs.
    //
    // Promise.resolve().then rather than queueMicrotask: the same microtask,
    // but available wherever Promise is, including the minimal environment
    // the shim's own tests run page.js in.
    Promise.resolve().then(() => {
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
