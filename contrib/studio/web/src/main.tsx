import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { ShortcutsProvider } from "./shortcuts/ShortcutsProvider";
import { applyTheme, storedTheme } from "./lib/theme";
import "./styles/tokens.css";
import "./styles/app.css";
import "./styles/shell.css";
import "./styles/grid.css";

// A saved choice wins over the system setting; without one the tokens follow
// prefers-color-scheme on their own.
const saved = storedTheme(window.localStorage);
if (saved) {
  applyTheme(document.documentElement, saved);
}

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing #root element");
}

createRoot(root).render(
  <StrictMode>
    <ShortcutsProvider>
      <App />
    </ShortcutsProvider>
  </StrictMode>,
);
