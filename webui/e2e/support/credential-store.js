// A recorder standing in for the browser's credential store.
//
// Elm's onSubmit prevents the default, so the browser never sees a submission and no manager
// offers to save. Magnes asks the credential store explicitly instead. The real API is
// Chromium-only and prompts, so a test installs this before the app loads rather than
// asserting against the browser's own store.
//
// Shared because the two halves of this belong to different suites: the refusal paths, where
// nothing may be offered, need no credential, while the success paths — the ones the call
// actually happens on — need the credentialed harness.

export async function recordCredentialStores(page) {
  await page.addInitScript(() => {
    window.__stored = [];
    window.PasswordCredential = class {
      constructor({ id, password }) {
        this.id = id;
        this.password = password;
      }
    };
    Object.defineProperty(navigator, "credentials", {
      configurable: true,
      value: {
        store: (credential) => {
          window.__stored.push({ id: credential.id, password: credential.password });
          return Promise.resolve(credential);
        },
      },
    });
  });
}

export function storedCredentials(page) {
  return page.evaluate(() => window.__stored);
}
