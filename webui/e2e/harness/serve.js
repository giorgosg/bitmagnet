// The credentialed suite's server side: one process that stands up everything a signed-in
// browser test needs, and takes it all down again.
//
// Playwright's `webServer` starts a command and waits for a URL, but it cannot read that
// command's stdout — and the bootstrap Invitation this suite registers through is printed
// on the fixture server's stdout. So the order is inverted here rather than split across a
// globalSetup: this script starts the fixture server, reads its announcement, registers the
// throwaway administrator, writes the credentials, and only then starts the dev server that
// Playwright is waiting on. By the time the first test runs, the credentials file exists.
//
// Nothing here is a secret at rest. The password is generated per run and the User it
// belongs to lives in a database that is dropped when this process exits, so the file is a
// handle on something already gone by the time anyone could read it. See e2e/README.md.

const childProcess = require("child_process");
const crypto = require("crypto");
const fs = require("fs");
const os = require("os");
const path = require("path");

const root = path.join(__dirname, "..", "..");
const bitmagnet = process.env.MAGNES_E2E_BITMAGNET || path.join(root, "..", "bitmagnet");
const testdb = process.env.MAGNES_E2E_TESTDB || path.join(root, "..", "btm-testdb");
const credentialsPath = process.env.MAGNES_E2E_CREDENTIALS;

// The settings the workflows branch on. Passed through rather than hardcoded so a project
// covering the other side of one — registration with invitations off, say — is a config
// entry rather than a change here.
const anonymousAccess = process.env.MAGNES_E2E_ANONYMOUS_ACCESS || "true";
const invitationRequired = process.env.MAGNES_E2E_INVITATION_REQUIRED || "true";

// The login throttle is turned off in all but name. bitmagnet buckets it by client address,
// and every request arrives from the development proxy, so the whole suite shares one
// bucket: at the shipped 30 a minute after a burst of 5, a handful of parallel tests that
// each sign in would start being refused for reasons none of them are about. A project that
// wants to *provoke* throttling sets these to 1 and asserts on the wait state.
const loginRequestsPerMinute = process.env.MAGNES_E2E_LOGIN_REQUESTS_PER_MINUTE || "6000";
const loginRequestBurst = process.env.MAGNES_E2E_LOGIN_REQUEST_BURST || "600";

// Everything this process is responsible for killing, newest first.
const shutdownSteps = [];
let shuttingDown = false;

async function main() {
  if (!credentialsPath) {
    throw new Error("MAGNES_E2E_CREDENTIALS must name where to write the credentials");
  }

  buildBundle();

  const templateDSN = seedTemplateDSN();
  const binary = buildFixtureServer();
  const announcement = await startFixtureServer(binary, templateDSN);
  const credentials = await registerAdministrator(announcement);

  fs.mkdirSync(path.dirname(credentialsPath), { recursive: true });
  fs.writeFileSync(
    credentialsPath,
    JSON.stringify(
      {
        // The pid is how e2e/harness/teardown.js finds this process to stop it politely.
        // Playwright's own teardown kills the web server hard enough that the database drop
        // does not finish, and a dropped connection is not the same as a dropped database.
        pid: process.pid,
        ...credentials,
        graphqlEndpoint: announcement.graphqlEndpoint,
      },
      null,
      2,
    ),
    { mode: 0o600 },
  );
  shutdownSteps.push(() => fs.rmSync(credentialsPath, { force: true }));

  startDevServer(announcement.address);
}

// The Elm bundle dev.js serves. Built here rather than by an npm script wrapping this one,
// because npm does not forward SIGTERM to the script it runs: with it in the middle, this
// process is never told to stop, and the fixture server's database is never dropped.
function buildBundle() {
  childProcess.execFileSync("npm", ["run", "build"], { cwd: root, stdio: "inherit" });
}

// The connection string for the instance holding the seed template. bitmagnet's dbtest
// clones from it; provisioning it is btm-testdb's job and not this suite's.
function seedTemplateDSN() {
  if (process.env.TEST_POSTGRES_TEMPLATE_DSN) return process.env.TEST_POSTGRES_TEMPLATE_DSN;

  const url = path.join(testdb, "bin", "testdb");
  if (!fs.existsSync(url)) {
    throw new Error(
      `no seed template: ${url} is missing, and TEST_POSTGRES_TEMPLATE_DSN is unset. ` +
        "See e2e/README.md.",
    );
  }

  try {
    return childProcess.execFileSync(url, ["url"], { encoding: "utf8" }).trim();
  } catch (error) {
    throw new Error(
      `could not ask btm-testdb for its connection string (${error.message}). ` +
        "Is the instance up? `bin/testdb status`.",
    );
  }
}

// Built rather than `go run`, because there is a signal to deliver later. `go run` is a
// parent that compiles and execs a second process, and killing the one whose pid we hold
// leaves the other serving — with a cloned database it will now never drop.
function buildFixtureServer() {
  if (!fs.existsSync(path.join(bitmagnet, "go.mod"))) {
    throw new Error(`no bitmagnet checkout at ${bitmagnet}. See e2e/README.md.`);
  }

  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "magnes-e2e-"));
  shutdownSteps.push(() => fs.rmSync(directory, { recursive: true, force: true }));

  const binary = path.join(directory, "fixture-server");
  childProcess.execFileSync("go", ["build", "-o", binary, "./internal/dev"], {
    cwd: bitmagnet,
    stdio: ["ignore", "ignore", "inherit"],
  });

  return binary;
}

// Starts the server and resolves with the one line of JSON it prints. Its stderr is passed
// through: gin and the logger write there deliberately so that stdout carries the
// announcement alone.
function startFixtureServer(binary, templateDSN) {
  const child = childProcess.spawn(
    binary,
    [
      "fixture",
      "serve",
      "--address",
      "127.0.0.1:0",
      `--anonymous-access=${anonymousAccess}`,
      `--invitation-required=${invitationRequired}`,
      `--login-requests-per-minute=${loginRequestsPerMinute}`,
      `--login-request-burst=${loginRequestBurst}`,
    ],
    {
      cwd: bitmagnet,
      env: { ...process.env, TEST_POSTGRES_TEMPLATE_DSN: templateDSN },
      stdio: ["ignore", "pipe", "inherit"],
    },
  );

  // Waited for on the way out, not just signalled: the clone is dropped in an fx OnStop
  // hook, so exiting before that finishes is how a stray bitmagnet_test_* database is left
  // behind on the fixture instance.
  shutdownSteps.push(() => stop(child, "the fixture server"));

  return new Promise((resolve, reject) => {
    let buffered = "";

    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      buffered += chunk;

      const newline = buffered.indexOf("\n");
      if (newline < 0) return;

      const line = buffered.slice(0, newline);
      buffered = buffered.slice(newline + 1);
      child.stdout.removeAllListeners("data");

      try {
        resolve(JSON.parse(line));
      } catch (error) {
        reject(new Error(`the fixture server announced something unreadable: ${line}`));
      }
    });

    child.on("exit", (code) => {
      reject(new Error(`the fixture server exited with ${code} before announcing itself`));
    });
    child.on("error", reject);
  });
}

// Claims the printed bootstrap Invitation, which makes this User an administrator — the
// first registration through it always is. Done over HTTP rather than in a browser because
// it has to happen before the browser has anything to talk to.
async function registerAdministrator(announcement) {
  const credentials = {
    username: `e2e-admin-${crypto.randomBytes(4).toString("hex")}`,
    // Comfortably past auth.password_min_entropy, and never the same twice.
    password: crypto.randomBytes(24).toString("base64url"),
  };

  const response = await fetch(announcement.graphqlEndpoint, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      query:
        "mutation Register($input: RegisterInput!) " +
        "{ self { register(input: $input) { user { username role } } } }",
      variables: {
        input: {
          invitationCode: announcement.invitationCode,
          username: credentials.username,
          password: credentials.password,
        },
      },
    }),
  });

  const body = await response.json();
  if (body.errors) {
    throw new Error(`registering the throwaway administrator: ${JSON.stringify(body.errors)}`);
  }

  const role = body.data?.self?.register?.user?.role;
  if (role !== "admin") {
    // Every credentialed spec assumes administration is reachable. Finding out here says
    // which assumption broke; finding out in a test says only that a screen was refused.
    throw new Error(`the bootstrap Invitation produced a ${role}, not an admin`);
  }

  return credentials;
}

// The same development proxy a person uses, pointed at the fixture. It terminates TLS and
// forwards /graphql with the browser's Host and Origin intact, which is what lets bitmagnet
// issue its Secure, SameSite=Strict cookie to a page on localhost.
function startDevServer(address) {
  const child = childProcess.spawn(process.execPath, [path.join(root, "dev.js")], {
    cwd: root,
    env: { ...process.env, BITMAGNET_URL: address },
    stdio: "inherit",
  });

  shutdownSteps.push(() => stop(child, "the dev server"));

  child.on("exit", (code) => {
    // Playwright is waiting on this server's URL; if it has gone there is nothing left to
    // wait for, and holding the fixture open would only leak a database.
    shutdown(code ?? 1);
  });
}

function stop(child, what) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve();

  return new Promise((resolve) => {
    const forced = setTimeout(() => {
      console.error(`${what} did not stop; killing it`);
      child.kill("SIGKILL");
    }, 10_000);

    child.on("exit", () => {
      clearTimeout(forced);
      resolve();
    });
    child.kill("SIGTERM");
  });
}

async function shutdown(code) {
  if (shuttingDown) return;
  shuttingDown = true;

  for (const step of shutdownSteps.reverse()) {
    try {
      await step();
    } catch (error) {
      console.error(`while shutting down: ${error.message}`);
    }
  }

  process.exit(code);
}

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => shutdown(0));
}

main().catch(async (error) => {
  console.error(`the credentialed harness could not start: ${error.message}`);
  await shutdown(1);
});
