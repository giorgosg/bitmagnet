// What the statistics pages' specs share: how a control is found, how a look is known to be
// drawn or answered, how the tab is hidden, and how an answer is held back. The pages are the
// torrent timeline and the queue's statistics (tickets 06 and 05 in .scratch/dashboard).

import { expect } from "./credentialed.js";

// A chip of the page's controls or filters, by its exact label: "minutes" is not "15 minutes".
export function chip(page, label) {
  return page.getByRole("button", { name: label, exact: true });
}

// The look is drawn, and none is on its way.
export async function drawn(page) {
  await expect(page.getByRole("figure").first()).toBeVisible();
  await expect(page.locator('.stats[aria-busy="false"]')).toBeVisible();
}

// Overrides what the page reads as its visibility, then tells it, the way the browser does when
// a tab is switched away from and back.
export async function setHidden(page, hidden) {
  await page.evaluate((value) => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => value });
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => (value ? "hidden" : "visible"),
    });
    document.dispatchEvent(new Event("visibilitychange"));
  }, hidden);
}

// For asserting that something did *not* happen: a request the page should not have made would
// have been sent by now. There is nothing to wait for in that case, so a real moment is given;
// never use it to wait for something that does happen.
export async function quietMoment(page) {
  await page.waitForTimeout(500);
}

// Two frames on, whatever the page was going to do with an answer it has been handed, it has
// done: Elm draws on the animation frame after it updates.
export async function afterFrames(page) {
  await page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
}

// Counts, from inside the page, the answers to requests whose body names `marker` that the page
// has finished receiving. Elm handles an answer as its request loads, so once this has counted
// one, the page has it: its timer will not wait on that look any more. Under a faked clock the
// drawing cannot say so, since the clock holds back the animation frames Elm draws on.
//
// Installed for the next navigation, and counted from nothing on each one. Resolves to a
// function that reads the count.
export async function answeredLooks(page, marker) {
  await page.addInitScript((needle) => {
    window.__answeredLooks = 0;
    const send = XMLHttpRequest.prototype.send;
    XMLHttpRequest.prototype.send = function (body) {
      if (typeof body === "string" && body.includes(needle)) {
        this.addEventListener("loadend", () => {
          window.__answeredLooks += 1;
        });
      }
      return send.call(this, body);
    };
  }, marker);
  return () => page.evaluate(() => window.__answeredLooks ?? 0);
}

// Holds back the first `count` requests `isLook` picks out, and answers each with `stale` once
// it is let go. `release()` lets them all go, and `delivered()` resolves when the last has
// arrived at the page, which it knows by `marker`, a string only the stale answer holds.
export async function holdLooks(page, { count, isLook, stale, marker }) {
  let release;
  const held = new Promise((resolve) => {
    release = resolve;
  });
  let remaining = count;
  await page.route("**/graphql", async (route) => {
    if (remaining > 0 && isLook(route.request())) {
      remaining -= 1;
      await held;
      await route.fulfill({ json: stale });
    } else {
      await route.continue();
    }
  });
  return {
    release,
    // The page has been handed the stale answer. Not the same as having done anything with it.
    delivered: () => page.waitForResponse(async (response) => (await response.text().catch(() => "")).includes(marker)),
  };
}
