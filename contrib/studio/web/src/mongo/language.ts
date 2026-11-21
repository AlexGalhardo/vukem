import type { EditorLanguage } from "../editor/Editor";
import { completeJson } from "./completion";
import { JsonLineCache } from "./json";

/** The editor language of the MongoDB console: JSON, with names completed from lists the page has. No comments. */
export function mongoLanguage(collections: readonly string[]): EditorLanguage {
  return {
    newCache: () => new JsonLineCache(),
    complete: (text, caret, explicit) => completeJson(text, caret, collections, explicit),
    statementAt: (text) => text.trim(),
    comments: false,
  };
}
