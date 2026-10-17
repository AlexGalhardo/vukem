// Vite plugin for Vukem apps (owned by your project, edit freely).
//
// It builds into resources/public/build with a manifest the Go side reads,
// and while `npm run dev` runs it writes resources/public/hot with the dev
// server URL, so {{vite}} in your views loads assets from the dev server.
import fs from "node:fs";
import path from "node:path";

export default function vukem({ input, publicDir = "resources/public", buildDir = "build" }) {
  const hotFile = path.resolve(publicDir, "hot");
  let devUrl = "";

  const removeHotFile = () => {
    if (fs.existsSync(hotFile)) fs.rmSync(hotFile);
  };

  return {
    name: "vukem",
    config(_, { command }) {
      return {
        base: command === "build" ? `/${buildDir}/` : "",
        publicDir: false,
        build: {
          manifest: true,
          outDir: path.join(publicDir, buildDir),
          emptyOutDir: true,
          rollupOptions: { input },
        },
        server: {
          // Asset URLs inside CSS and JS must point at the dev server.
          origin: "__vukem_dev_origin__",
        },
      };
    },
    configureServer(server) {
      server.httpServer?.once("listening", () => {
        const address = server.httpServer.address();
        const host = typeof address === "object" && address && !["::", "0.0.0.0", "::1"].includes(address.address)
          ? address.address
          : "localhost";
        devUrl = `${server.config.server.https ? "https" : "http"}://${host}:${address.port}`;
        fs.writeFileSync(hotFile, devUrl);
      });

      for (const signal of ["exit", "SIGINT", "SIGTERM", "SIGHUP"]) {
        process.on(signal, () => {
          removeHotFile();
          if (signal !== "exit") process.exit();
        });
      }
    },
    transform(code) {
      return devUrl ? code.replaceAll("__vukem_dev_origin__", devUrl) : undefined;
    },
  };
}
