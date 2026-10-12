import path from "node:path";
import { defineConfig, devices } from "@playwright/test";

// This server runs migrations: callers must supply a disposable database.
const dsn = process.env.BUNDLED_UI_POSTGRES_DSN;
if (!dsn)
  throw new Error("BUNDLED_UI_POSTGRES_DSN must name a disposable database");

const port = Number(process.env.BUNDLED_UI_PORT || 8335);
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: "e2e/bundled",
  forbidOnly: !!process.env.CI,
  use: { ...devices["Desktop Chrome"], baseURL },
  webServer: {
    command: "../bitmagnet worker run --keys=http_server",
    port,
    reuseExistingServer: false,
    timeout: 120_000,
    env: {
      POSTGRES_DSN: dsn,
      HTTP_SERVER_LOCAL_ADDRESS: `127.0.0.1:${port}`,
      HTTP_SERVER_OPTIONS: "auth,graphql,static",
      AUTH_ANONYMOUS_ACCESS: "false",
      XDG_CONFIG_HOME: path.resolve(".dev/bundled-config"),
    },
  },
});
