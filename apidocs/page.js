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
// so its <style> elements and style attributes stay blocked, except the
// numeric line-number variables of highlighted code samples, re-applied
// from an allowlist.
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
      if (String(name).toLowerCase() === "style" && this.style && safeStyle(String(value))) {
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
