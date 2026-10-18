// Unit test of page.js against a minimal fake DOM; run by TestPageShim
// (node --test testdata/shim_test.mjs).
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const source = readFileSync(new URL("../page.js", import.meta.url), "utf8");

// Labelable elements, for the fake <label>.control.
const labelable = new Set(["BUTTON", "INPUT", "METER", "OUTPUT", "PROGRESS", "SELECT", "TEXTAREA"]);

// A fake matcher for the compound selectors page.js uses: a tag name,
// .class, [attr], [attr="value"] and :not(...) of those.
function matches(el, sel) {
  const tokens = sel.match(/:not\([^)]*\)|\[[^\]]*\]|\.[A-Za-z0-9_-]+|[A-Za-z][A-Za-z0-9-]*/g) || [];

  return tokens.every((token) => {
    if (token.startsWith(":not(")) return !matches(el, token.slice(5, -1));
    if (token.startsWith("[")) {
      const [, name, value] = token.slice(1, -1).match(/^([^=]+)(?:=["']?([^"']*)["']?)?$/);
      const got = el.getAttribute(name);
      return value === undefined ? got !== null : got === value;
    }
    if (token.startsWith(".")) return (el.getAttribute("class") || "").split(/\s+/).includes(token.slice(1));

    return el.tagName === token.toUpperCase();
  });
}

function load(nonce) {
  const attrs = [];
  const observers = [];
  const all = [];
  // Every element document.querySelectorAll can find, in document order.
  const tree = [];

  class Element {
    constructor(tag) {
      this.tagName = tag.toUpperCase();
      this.attributes = new Map();
      this.style = { cssText: "", length: 0, props: {}, setProperty(k, v) { this.props[k] = v; this.length++; } };
      this.textContent = "";
      this.className = "";
      this.childNodes = [];
      this.parentNode = null;
    }
    get control() {
      const forId = this.getAttribute("for");
      if (forId !== null) return tree.find((el) => el.getAttribute("id") === forId) || null;

      return this.childNodes.find((el) => labelable.has(el.tagName)) || null;
    }
    getAttributeNames() {
      return [...this.attributes.keys()];
    }
    append(...nodes) {
      for (const node of nodes) {
        node.parentNode?.childNodes.splice(node.parentNode.childNodes.indexOf(node), 1);
        node.parentNode = this;
        this.childNodes.push(node);
      }
    }
    replaceWith(node) {
      const inTree = tree.indexOf(this);
      if (inTree >= 0) tree[inTree] = node;

      const inParent = this.parentNode ? this.parentNode.childNodes.indexOf(this) : -1;
      if (inParent >= 0) this.parentNode.childNodes[inParent] = node;

      node.parentNode = this.parentNode;
      this.parentNode = null;
    }
    setAttribute(name, value) {
      attrs.push([name, String(value)]);
      this.attributes.set(name, String(value));
    }
    getAttribute(name) {
      return this.attributes.has(name) ? this.attributes.get(name) : null;
    }
    removeAttribute(name) {
      this.attributes.delete(name);
    }
    // A fake parser: every data-vukem-line-vars attribute in the markup
    // becomes a child element carrying it.
    set innerHTML(html) {
      this.html = html;
      this.children = [...String(html).matchAll(/data-vukem-line-vars="([^"]*)"/g)].map(([, css]) => {
        const el = new Element("code");
        el.attributes.set("data-vukem-line-vars", css);
        return el;
      });
    }
    get innerHTML() {
      return this.html;
    }
    querySelectorAll(sel) {
      assert.equal(sel, "[data-vukem-line-vars]");
      return (this.children || []).filter((el) => el.attributes.has("data-vukem-line-vars"));
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
    // page.js marks the document once its pass has run, so a test can tell
    // "the pass ran" from "the page never rendered".
    documentElement: {
      attrs: {},
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
      getAttribute(name) {
        return name in this.attrs ? this.attrs[name] : null;
      },
    },
    querySelectorAll: (sel) => tree.filter((el) => matches(el, sel)),
  });

  // Adds an element to the fake document and returns it.
  const add = (tag, attributes = {}, children = []) => {
    const el = new Element(tag);
    for (const [name, value] of Object.entries(attributes)) el.attributes.set(name, String(value));
    el.append(...children);
    tree.push(el);

    return el;
  };

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
  ctx.globalThis = ctx;
  vm.createContext(ctx);
  vm.runInContext(source, ctx);

  // The shim queues its accessibility pass on a mutation; the fake
  // page.js defers its pass to a microtask (a frame may never be painted in
  // a headless dump), so firing the observers is not enough: the queue has to
  // drain before the DOM reflects the pass. A macrotask tick guarantees that
  // without depending on how many microtasks the pass itself schedules.
  const fix = async () => {
    observers.forEach((observer) => observer());
    await new Promise((resolve) => setTimeout(resolve, 0));
  };

  return { document, attrs, observers, all, tree, add, fix, globalThis: ctx };
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

test("zod is made jitless so it never probes eval", () => {
  const ctx = load("N");
  assert.equal(ctx.globalThis.__zod_globalConfig.jitless, true);
  assert.equal(load("").globalThis.__zod_globalConfig, undefined);
});

test("innerHTML moves only exact line-number variables out of style attributes", () => {
  const { document } = load("N");
  const el = document.createElement("div");
  el.innerHTML = '<code class="hljs" style="--line-count: 12; --line-digits: 2;">x</code>';
  assert.equal(el.html, '<code class="hljs" data-vukem-line-vars="--line-count: 12; --line-digits: 2;">x</code>');
  assert.equal(el.children.length, 1);
  assert.deepEqual(el.children[0].style.props, { "--line-count": "12", "--line-digits": "2" });
  assert.equal(el.children[0].getAttribute("data-vukem-line-vars"), null);

  const hostile = [
    '<p style="--line-count: 1; color: red;">x</p>',
    '<p style="--line-count: 1; background:url(/x)">x</p>',
    '<p style="--line-count: calc(1px)">x</p>',
    '<p style="display:none">--line-count</p>',
  ];
  for (const html of hostile) {
    el.innerHTML = html;
    assert.equal(el.html, html, html);
  }

  el.innerHTML = null;
  assert.equal(el.html, null);
});

test("innerHTML is left alone without a nonce", () => {
  const { document } = load("");
  const el = document.createElement("div");
  const html = '<code style="--line-count: 1;">x</code>';
  el.innerHTML = html;
  assert.equal(el.html, html);
});

test("a caption <label> that labels nothing is retagged as a span", async () => {
  const { add, fix, tree } = load("N");
  const text = { tagName: "#TEXT", parentNode: null, childNodes: [] };
  const caption = add("label", { class: "bg-b-2 font-medium", "data-v-dfab866f": "" }, [text]);

  await fix();

  const span = tree[0];
  assert.equal(span.tagName, "SPAN");
  assert.notEqual(span, caption);
  assert.deepEqual(span.getAttributeNames(), ["class", "data-v-dfab866f"]);
  assert.equal(span.getAttribute("class"), "bg-b-2 font-medium");
  assert.deepEqual(span.childNodes, [text]);
  assert.equal(text.parentNode, span);
  // Idempotent: a second pass finds no <label> left to retag.
  await fix();
  assert.equal(tree.length, 1);
  assert.equal(tree[0], span);
});

test("a caption's inline style is copied through the CSSOM, not setAttribute", async () => {
  const { add, fix, tree, attrs } = load("N");
  const caption = add("label", { class: "c", style: "width: 10px" });
  caption.style.cssText = "width: 10px";

  await fix();

  assert.equal(tree[0].tagName, "SPAN");
  assert.equal(tree[0].style.cssText, "width: 10px");
  assert.deepEqual(attrs, [["class", "c"]]);
});

test("labels that label something are left alone", async () => {
  const { add, fix, tree } = load("N");
  const field = add("input", { id: "token" });
  const explicit = add("label", { for: "token" });
  const missing = add("label", { for: "not-mounted-yet" });
  const nested = add("label", { class: "scalar-card-checkbox" }, [add("input", { type: "checkbox" })]);

  await fix();

  assert.deepEqual(tree.filter((el) => el.tagName === "LABEL"), [explicit, missing, nested]);
  assert.equal(field.parentNode, null);
});

test("an unnamed combobox field gets an accessible name, a named one keeps its own", async () => {
  const { add, fix } = load("N");
  const unnamed = add("input", { role: "combobox" });
  const labelled = add("input", { role: "combobox", "aria-label": "Search clients" });
  const described = add("input", { role: "combobox", "aria-labelledby": "heading" });
  const placeheld = add("input", { role: "combobox", placeholder: "Search..." });
  const titled = add("input", { role: "combobox", title: "Search" });
  const plain = add("input", { type: "text" });

  await fix();

  assert.equal(unnamed.getAttribute("aria-label"), "Search");
  assert.equal(labelled.getAttribute("aria-label"), "Search clients");
  assert.equal(described.getAttribute("aria-label"), null);
  assert.equal(placeheld.getAttribute("aria-label"), null);
  assert.equal(titled.getAttribute("aria-label"), null);
  assert.equal(plain.getAttribute("aria-label"), null);
});

test("the scrollable endpoint list and the Show More buttons keep their fixes", async () => {
  const { add, fix } = load("N");
  const list = add("div", { class: "endpoints" });
  const button = add("button", { class: "show-more", "aria-label": "7 more operations" });
  button.textContent = "Show More";
  const already = add("button", { class: "show-more", "aria-label": "Show More: 7 more operations" });
  already.textContent = "Show More";

  await fix();

  assert.equal(list.tabIndex, 0);
  assert.equal(button.getAttribute("aria-label"), "Show More: 7 more operations");
  assert.equal(already.getAttribute("aria-label"), "Show More: 7 more operations");
});
