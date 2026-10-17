import type { Plugin } from "vite";

export interface VukemOptions {
  /** Entry points, e.g. ["resources/css/app.css", "resources/js/app.ts"]. */
  input: string | string[];
  /** Directory served by the Go app (default "resources/public"). */
  publicDir?: string;
  /** Build directory inside publicDir (default "build"). */
  buildDir?: string;
}

export default function vukem(options: VukemOptions): Plugin;
