import { useCallback, useEffect, useId, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, KeyboardEvent, ReactElement, Ref } from "react";
import { CloseIcon } from "../components/icons";
import { complete } from "./complete";
import type { Completion, CompletionSource } from "./complete";
import { bracketMatch, commentEdit, findMatches, indentEdit, lineOf, newlineEdit, statementAt, visualColumn } from "./ops";
import type { Edit, Match } from "./ops";
import { LineCache } from "./tokenizer";
import type { Token } from "./tokenizer";

/** Metrics shared by the textarea, the highlight layer and the line numbers: they must be identical for the layers to line up. */
export const lineHeight = 20;
export const padding = 8;
const overscan = 6;

export type RunScope = "cursor" | "all";

export interface EditorHandle {
  focus: () => void;
  /** The text a run would send: the selection, else the statement at the cursor (or all the text). Empty when there is none. */
  statement: (scope: RunScope) => string;
  /** Replaces the whole text and puts the caret at its end. */
  insert: (text: string) => void;
}

export interface EditorProps {
  value: string;
  onChange: (value: string) => void;
  /** The accessible name of the text area. */
  label: string;
  completion: CompletionSource;
  onRun: (scope: RunScope) => void;
  onSave: () => void;
  ref?: Ref<EditorHandle>;
}

function applyEdit(ta: HTMLTextAreaElement, edit: Edit): void {
  ta.focus();
  ta.setSelectionRange(edit.from, edit.to);
  // execCommand keeps the browser's undo history; setRangeText is the fallback where it is refused.
  const done = edit.insert !== "" && document.execCommand("insertText", false, edit.insert);
  if (!done) {
    ta.setRangeText(edit.insert, edit.from, edit.to, "end");
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  }
  ta.setSelectionRange(edit.selStart, edit.selEnd);
}

function HighlightedLine({ tokens }: { tokens: readonly Token[] }): ReactElement {
  return (
    <>
      {tokens.map((t, i) =>
        t.kind === "space" ? (
          t.text
        ) : (
          <span key={i} className={`tok-${t.kind}`}>
            {t.text}
          </span>
        ),
      )}
    </>
  );
}

interface FindState {
  open: boolean;
  query: string;
  current: number;
}

/**
 * Editor is the SQL editor: a real textarea (selection, IME, undo, mobile and
 * assistive technology work as they do everywhere) with a highlight layer
 * behind it. The layer and the line numbers draw only the lines in view, from a
 * line cache, so typing in a long text costs about one line.
 */
export function Editor({ value, onChange, label, completion, onRun, onSave, ref }: EditorProps): ReactElement {
  const taRef = useRef<HTMLTextAreaElement>(null);
  const findRef = useRef<HTMLInputElement>(null);
  const probe = useRef<HTMLSpanElement>(null);
  const cache = useRef(new LineCache());
  const escaped = useRef(false);
  const blurSel = useRef<{ start: number; end: number } | null>(null);
  const id = useId();

  const [scroll, setScroll] = useState({ top: 0, left: 0 });
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [charW, setCharW] = useState(7.8);
  const [sel, setSel] = useState({ start: 0, end: 0 });
  const [find, setFind] = useState<FindState>({ open: false, query: "", current: 0 });
  const [pop, setPop] = useState<{ result: Completion; active: number } | null>(null);
  const [hasFocus, setHasFocus] = useState(false);

  const lines = useMemo(() => cache.current.update(value), [value]);
  const rawLines = useMemo(() => value.split("\n"), [value]);
  const lineCount = lines.length;
  const digits = String(lineCount).length;

  useImperativeHandle(
    ref,
    () => ({
      focus: () => {
        const ta = taRef.current;
        if (!ta) {
          return;
        }
        ta.focus();
        // After a modal dialog closes the browser can hand focus back with the caret reset and Ctrl+A dead: put the caret back where it was.
        const at = blurSel.current;
        if (at) {
          ta.setSelectionRange(Math.min(at.start, ta.value.length), Math.min(at.end, ta.value.length));
        }
      },
      statement: (scope) => {
        const ta = taRef.current;
        const text = ta?.value ?? value;
        const s = ta?.selectionStart ?? 0;
        const e = ta?.selectionEnd ?? 0;
        if (s !== e) {
          return text.slice(s, e).trim();
        }
        if (scope === "all") {
          return text.trim();
        }
        const r = statementAt(text, s);
        return r ? text.slice(r.start, r.end) : "";
      },
      insert: (text) => {
        const ta = taRef.current;
        if (ta) {
          applyEdit(ta, { from: 0, to: ta.value.length, insert: text, selStart: text.length, selEnd: text.length });
        } else {
          onChange(text);
        }
      },
    }),
    [value, onChange],
  );

  useLayoutEffect(() => {
    const ta = taRef.current;
    if (!ta) {
      return;
    }
    const measure = (): void => {
      setSize({ width: ta.clientWidth, height: ta.clientHeight });
      const p = probe.current;
      if (p) {
        const w = p.getBoundingClientRect().width / 10;
        if (w > 0) {
          setCharW(w);
        }
      }
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(ta);
    return () => observer.disconnect();
  }, []);

  const caret = useMemo(() => lineOf(value, sel.start), [value, sel.start]);
  const matches: Match[] = useMemo(() => (find.open ? findMatches(value, find.query) : []), [find.open, find.query, value]);

  const brackets = useMemo(
    () => (sel.start === sel.end ? bracketMatch(lines, { line: caret.line, col: caret.col }) : null),
    [lines, sel.start, sel.end, caret.line, caret.col],
  );

  const first = Math.max(Math.floor(scroll.top / lineHeight) - overscan, 0);
  const last = Math.min(Math.ceil((scroll.top + size.height) / lineHeight) + overscan, lineCount);

  const boxAt = (line: number, col: number, len: number, className: string, key: string): ReactElement => {
    const text = rawLines[line] ?? "";
    const left = visualColumn(text, col);
    const width = visualColumn(text, col + len) - left;
    return (
      <span
        key={key}
        className={className}
        style={{ top: line * lineHeight + padding, left: `calc(var(--sqled-pad) + ${left}ch)`, width: `${width}ch` }}
      />
    );
  };

  const boxes: ReactElement[] = [];
  const lineText = (n: number): string => rawLines[n] ?? "";
  if (brackets) {
    for (const p of brackets) {
      boxes.push(boxAt(p.line, p.col, 1, "sqled-box sqled-box-bracket", `b${p.line}:${p.col}`));
    }
  }
  matches.forEach((m, i) => {
    if (m.line >= first && m.line < last) {
      boxes.push(boxAt(m.line, m.col, m.len, i === find.current ? "sqled-box sqled-box-current" : "sqled-box sqled-box-match", `m${i}`));
    }
  });

  const reveal = useCallback((line: number, col: number): void => {
    const ta = taRef.current;
    if (!ta) {
      return;
    }
    const top = line * lineHeight;
    if (top < ta.scrollTop || top + lineHeight * 2 > ta.scrollTop + ta.clientHeight) {
      ta.scrollTop = Math.max(top - ta.clientHeight / 2, 0);
    }
    const left = col * charW;
    if (left < ta.scrollLeft || left + charW * 4 > ta.scrollLeft + ta.clientWidth - padding * 2) {
      ta.scrollLeft = Math.max(left - ta.clientWidth / 2, 0);
    }
  }, [charW]);

  const goToMatch = useCallback(
    (index: number, list: Match[]): void => {
      const m = list[index];
      const ta = taRef.current;
      if (!m || !ta) {
        return;
      }
      ta.setSelectionRange(m.index, m.index + m.len);
      reveal(m.line, m.col);
      setSel({ start: m.index, end: m.index + m.len });
    },
    [reveal],
  );

  const refreshCompletion = (explicit: boolean): void => {
    const ta = taRef.current;
    if (!ta || ta.selectionStart !== ta.selectionEnd) {
      setPop(null);
      return;
    }
    const result = complete(ta.value, ta.selectionStart, completion, explicit);
    setPop(result.items.length > 0 ? { result, active: 0 } : null);
  };

  const accept = (index: number): void => {
    const ta = taRef.current;
    const item = pop?.result.items[index];
    if (!ta || !pop || !item) {
      return;
    }
    const to = ta.selectionStart;
    const at = pop.result.from + item.label.length;
    // A keyword completes in the case the person was typing in.
    const label = item.kind === "keyword" && ta.value.slice(pop.result.from, to) === ta.value.slice(pop.result.from, to).toLowerCase() ? item.label.toLowerCase() : item.label;
    applyEdit(ta, { from: pop.result.from, to, insert: label, selStart: at, selEnd: at });
    setPop(null);
  };

  const openFind = (): void => {
    const ta = taRef.current;
    const selected = ta && ta.selectionStart !== ta.selectionEnd ? ta.value.slice(ta.selectionStart, ta.selectionEnd) : "";
    setFind((f) => ({ open: true, query: selected !== "" && !selected.includes("\n") ? selected : f.query, current: 0 }));
    setPop(null);
    requestAnimationFrame(() => {
      findRef.current?.focus();
      findRef.current?.select();
    });
  };

  const closeFind = (): void => {
    setFind((f) => ({ ...f, open: false }));
    taRef.current?.focus();
  };

  const stepFind = (delta: number): void => {
    if (matches.length === 0) {
      return;
    }
    const next = (find.current + delta + matches.length) % matches.length;
    setFind((f) => ({ ...f, current: next }));
    goToMatch(next, matches);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>): void => {
    const ta = e.currentTarget;
    if (e.nativeEvent.isComposing || e.keyCode === 229) {
      return;
    }
    const mod = e.ctrlKey || e.metaKey;
    const text = ta.value;
    const key = e.key;
    const wasEscaped = escaped.current;

    if (key !== "Shift" && key !== "Control" && key !== "Alt" && key !== "Meta" && key !== "Escape") {
      escaped.current = false;
    }

    if (pop) {
      const n = pop.result.items.length;
      if (key === "ArrowDown" || key === "ArrowUp") {
        e.preventDefault();
        setPop({ ...pop, active: (pop.active + (key === "ArrowDown" ? 1 : -1) + n) % n });
        return;
      }
      const typed = text.slice(pop.result.from, ta.selectionStart).toLowerCase();
      const same = pop.result.items[pop.active]?.label.toLowerCase() === typed;
      if (key === "Enter" && same) {
        // Accepting what is already typed would only swallow the new line.
        setPop(null);
      } else if ((key === "Enter" || key === "Tab") && !mod && !e.shiftKey && !e.altKey) {
        e.preventDefault();
        accept(pop.active);
        return;
      }
      if (key === "Escape") {
        e.preventDefault();
        setPop(null);
        return;
      }
      if (["ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown"].includes(key)) {
        setPop(null);
      }
    }

    if (mod && key === "Enter") {
      e.preventDefault();
      onRun(e.shiftKey ? "all" : "cursor");
      return;
    }
    if (mod && key === "/") {
      e.preventDefault();
      applyEdit(ta, commentEdit(text, ta.selectionStart, ta.selectionEnd));
      return;
    }
    if (mod && !e.shiftKey && (key === "s" || key === "S")) {
      e.preventDefault();
      onSave();
      return;
    }
    if (mod && !e.shiftKey && (key === "f" || key === "F")) {
      e.preventDefault();
      openFind();
      return;
    }
    if (e.ctrlKey && !e.metaKey && !e.altKey && key === " ") {
      e.preventDefault();
      refreshCompletion(true);
      return;
    }
    if (key === "Escape") {
      if (find.open) {
        closeFind();
      } else {
        escaped.current = true;
      }
      return;
    }
    if (key === "Tab" && !mod && !e.altKey) {
      if (wasEscaped) {
        return;
      }
      const edit = indentEdit(text, ta.selectionStart, ta.selectionEnd, e.shiftKey);
      e.preventDefault();
      if (edit) {
        applyEdit(ta, edit);
      }
      return;
    }
    if (key === "Enter" && !mod && !e.altKey) {
      e.preventDefault();
      applyEdit(ta, newlineEdit(text, ta.selectionStart, ta.selectionEnd));
    }
  };

  const onFindKeyDown = (e: KeyboardEvent<HTMLInputElement>): void => {
    if (e.nativeEvent.isComposing || e.keyCode === 229) {
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      stepFind(e.shiftKey ? -1 : 1);
    } else if (e.key === "Escape") {
      e.preventDefault();
      closeFind();
    }
  };

  // Mod+F and Mod+S belong to the page outside the editor; inside it they open the find bar and save the query.
  const onRootKeyDown = (e: KeyboardEvent<HTMLDivElement>): void => {
    if (e.target === taRef.current) {
      return;
    }
    const mod = e.ctrlKey || e.metaKey;
    if (mod && !e.shiftKey && (e.key === "f" || e.key === "F")) {
      e.preventDefault();
      openFind();
    } else if (mod && !e.shiftKey && (e.key === "s" || e.key === "S")) {
      e.preventDefault();
      onSave();
    }
  };

  useEffect(() => {
    // Columns that arrive after "table." was typed complete it then.
    const ta = taRef.current;
    if (ta && document.activeElement === ta && ta.selectionStart === ta.selectionEnd && ta.value[ta.selectionStart - 1] === ".") {
      const result = complete(ta.value, ta.selectionStart, completion, false);
      setPop(result.items.length > 0 ? { result, active: 0 } : null);
    }
  }, [completion]);

  useEffect(() => {
    // Keep the completion list from outliving a move of the caret to another word.
    if (pop && sel.start !== sel.end) {
      setPop(null);
    }
  }, [pop, sel.start, sel.end]);

  const popLine = lineOf(value, sel.start);
  const popLeft = Math.min(
    padding + visualColumn(lineText(popLine.line), pop ? pop.result.from - (sel.start - popLine.col) : 0) * charW - scroll.left,
    Math.max(size.width - 200, 0),
  );
  const popTop = (popLine.line + 1) * lineHeight + padding - scroll.top;

  const style = { "--sqled-lh": `${lineHeight}px`, "--sqled-pad": `${padding}px`, "--sqled-digits": digits } as CSSProperties;
  const listId = `${id}-list`;

  return (
    <div className="sqled" style={style} onKeyDown={onRootKeyDown}>
      {find.open ? (
        <div className="sqled-find" role="search" aria-label="Find in the query">
          <input
            ref={findRef}
            className="sqled-find-input"
            type="text"
            aria-label="Find"
            value={find.query}
            spellCheck={false}
            autoComplete="off"
            onChange={(e) => {
              const query = e.target.value;
              setFind({ open: true, query, current: 0 });
              const list = findMatches(value, query);
              if (list.length > 0) {
                goToMatch(0, list);
              }
            }}
            onKeyDown={onFindKeyDown}
          />
          <button type="button" className="button button-small" onClick={() => stepFind(-1)} disabled={matches.length === 0}>
            Previous
          </button>
          <button type="button" className="button button-small" onClick={() => stepFind(1)} disabled={matches.length === 0}>
            Next
          </button>
          <span className="muted sqled-find-count" role="status">
            {find.query === "" ? "" : matches.length === 0 ? "No matches" : `${find.current + 1} of ${matches.length}`}
          </span>
          <button type="button" className="icon-button" aria-label="Close find" onClick={closeFind}>
            <CloseIcon />
          </button>
        </div>
      ) : null}
      <div className={hasFocus ? "sqled-frame sqled-focused" : "sqled-frame"}>
        <div className="sqled-gutter" aria-hidden="true">
          <div className="sqled-gutter-inner" style={{ transform: `translateY(${-scroll.top}px)` }}>
            {Array.from({ length: last - first }, (_, k) => first + k).map((n) => (
              <div
                key={n}
                className={n === caret.line ? "sqled-num sqled-num-current" : "sqled-num"}
                style={{ top: n * lineHeight + padding }}
              >
                {n + 1}
              </div>
            ))}
          </div>
        </div>
        <div className="sqled-body">
          <div className="sqled-clip" aria-hidden="true">
            {hasFocus && sel.start === sel.end ? (
              <div className="sqled-current-line" style={{ top: caret.line * lineHeight + padding - scroll.top }} />
            ) : null}
            <div className="sqled-layer" style={{ transform: `translate(${-scroll.left}px, ${-scroll.top}px)` }}>
              {boxes}
              {Array.from({ length: last - first }, (_, k) => first + k).map((n) => (
                <div key={n} className="sqled-line" style={{ top: n * lineHeight + padding }}>
                  <HighlightedLine tokens={lines[n] ?? []} />
                </div>
              ))}
            </div>
            <span ref={probe} className="sqled-probe">
              0000000000
            </span>
          </div>
          <textarea
            ref={taRef}
            className="sqled-input"
            aria-label={label}
            aria-describedby={`${id}-hint`}
            aria-autocomplete="list"
            aria-controls={pop ? listId : undefined}
            aria-activedescendant={pop ? `${listId}-${pop.active}` : undefined}
            value={value}
            wrap="off"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
            onChange={(e) => {
              onChange(e.target.value);
              const nativeType = (e.nativeEvent as InputEvent).inputType;
              if (nativeType?.startsWith("insert") && nativeType !== "insertLineBreak") {
                refreshCompletion(false);
              } else {
                setPop(null);
              }
            }}
            onKeyDown={onKeyDown}
            onSelect={(e) => setSel({ start: e.currentTarget.selectionStart, end: e.currentTarget.selectionEnd })}
            onScroll={(e) => setScroll({ top: e.currentTarget.scrollTop, left: e.currentTarget.scrollLeft })}
            onFocus={() => setHasFocus(true)}
            onBlur={(e) => {
              blurSel.current = { start: e.currentTarget.selectionStart, end: e.currentTarget.selectionEnd };
              setHasFocus(false);
              setPop(null);
            }}
            onMouseDown={() => setPop(null)}
          />
          {pop ? (
            <ul id={listId} className="sqled-pop" role="listbox" aria-label="Completions" style={{ left: popLeft, top: popTop }}>
              {pop.result.items.map((item, i) => (
                <li
                  key={`${item.kind}:${item.label}`}
                  id={`${listId}-${i}`}
                  role="option"
                  aria-selected={i === pop.active}
                  className={i === pop.active ? "sqled-option sqled-option-active" : "sqled-option"}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    accept(i);
                  }}
                >
                  <span className="truncate">{item.label}</span>
                  <span className="sqled-option-kind">{item.kind}</span>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </div>
      <div className="sqled-status">
        <p id={`${id}-hint`} className="muted sqled-hint">
          Tab indents. Press Escape, then Tab, to leave the editor.
        </p>
        <span className="muted sqled-pos">
          Line {caret.line + 1}, column {caret.col + 1}
        </span>
      </div>
      <div className="visually-hidden" role="status" aria-live="polite">
        {pop ? `${pop.result.items.length} suggestions` : ""}
      </div>
    </div>
  );
}
