// Unit test of page.js against a minimal fake DOM; run by TestPageShim
// (node --test testdata/shim_test.mjs).
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const source = readFileSync(new URL("../page.js", import.meta.url), "utf8");

function load(nonce) {
  const attrs = [];
  const observers = [];
  const all = [];

  class Element {
    constructor(tag) {
      this.tagName = tag.toUpperCase();
      this.attributes = new Map();
      this.style = { cssText: "", length: 0, props: {}, setProperty(k, v) { this.props[k] = v; this.length++; } };
      this.textContent = "";
      this.className = "";
    }
    setAttribute(name, value) {
      attrs.push([name, String(value)]);
      this.attributes.set(name, String(value));
    }
    getAttribute(name) {
      return this.attributes.has(name) ? this.attributes.get(name) : null;
    }
  }

  class Document {
    createElement(tag) {
      const el = new Element(tag);
      all.push(el);
      return el;
    }
  }

  const document = Object.assign(new Document(), {
    currentScript: { nonce },
    documentElement: {},
    querySelectorAll: () => [],
  });

  const ctx = {
    Document,
    Element,
    document,
    MutationObserver: class {
      constructor(fn) { observers.push(fn); }
      observe() {}
    },
    requestAnimationFrame: (fn) => fn(),
  };
  vm.createContext(ctx);
  vm.runInContext(source, ctx);

  return { document, attrs, observers, all };
}

test("created style elements get the nonce, other elements do not", () => {
  const { document } = load("N0nce");
  assert.equal(document.createElement("style").nonce, "N0nce");
  assert.equal(document.createElement("STYLE").nonce, "N0nce");
  assert.equal(document.createElement("div").nonce, undefined);
  assert.equal(document.createElement("script").nonce, undefined);
});

test("without a nonce nothing is patched", () => {
  const { document, attrs } = load("");
  assert.equal(document.createElement("style").nonce, undefined);
  const el = document.createElement("div");
  el.setAttribute("style", "width:30px");
  assert.deepEqual(attrs, [["style", "width:30px"]]);
  assert.equal(el.style.cssText, "");
});

test("only plain size and overflow style attributes go through the CSSOM", () => {
  const { document, attrs } = load("N");
  const safe = ["width:30px;height:30px;overflow-y:scroll;", "width:100%;height:40px", "max-height: 10rem"];
  for (const css of safe) {
    const el = document.createElement("div");
    el.setAttribute("style", css);
    assert.equal(el.style.cssText, css, css);
  }
  assert.deepEqual(attrs, []);

  const hostile = [
    "background:url(/x)",
    "width:30px;background-image:url(https://evil.example/x)",
    "position:fixed;inset:0;z-index:9999",
    "display:none",
    "width:expression(alert(1))",
    "width:var(--x)",
    "height:1px;}*{display:none",
    "width\\:1px",
  ];
  for (const css of hostile) {
    const el = document.createElement("div");
    el.setAttribute("style", css);
    assert.equal(el.style.cssText, "", css);
  }
  assert.deepEqual(attrs.map(([, v]) => v), hostile);
});

test("other attributes are set normally", () => {
  const { document, attrs } = load("N");
  document.createElement("div").setAttribute("class", "x");
  assert.deepEqual(attrs, [["class", "x"]]);
});
