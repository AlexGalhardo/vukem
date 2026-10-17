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
// Accessibility: fixes the axe "serious" findings Scalar's markup has
// (scrollable endpoint list without keyboard access, "Show More" buttons
// whose accessible name does not contain their visible text).
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
