// Shares: the setup intro for each of the four mode / modeReason cases. Node runs these (npm test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { shareIntro } from "./shareIntro.ts";

const PROXMOX_COMPOSE = "Mount the share on the Proxmox host, pass the folder into the container, then add this line to your compose file and restart it.";
const PROXMOX_MANUAL = "Run these steps on the Proxmox host, as root.";

test("docker in an unprivileged container keeps the Proxmox-host compose wording", () => {
  const [intro, after] = shareIntro("docker", "unprivileged_container");
  assert.ok(intro.includes(PROXMOX_COMPOSE));
  assert.equal(after, "The Proxmox host keeps it connected. Press Check again once the container is back up.");
});

test("docker elsewhere gets the plain compose wording", () => {
  assert.deepEqual(shareIntro("docker", null), [
    "Add these lines to your compose file, then restart the container.",
    "Docker connects it each time the container starts. Press Check again once it's back up.",
  ]);
});

test("manual in an unprivileged container gets the Proxmox-host steps wording", () => {
  const [intro, after] = shareIntro("manual", "unprivileged_container");
  assert.ok(intro.includes(PROXMOX_MANUAL));
  assert.ok(!intro.includes("compose"));
  assert.equal(after, "The Proxmox host keeps it connected. Press Check again once the container is back up.");
});

test("otherwise gets the run-on-this-server wording", () => {
  assert.deepEqual(shareIntro("manual", null), ["Run this once on this server, as root:", "It connects the share now and after every restart."]);
  assert.deepEqual(shareIntro("app", undefined), ["Run this once on this server, as root:", "It connects the share now and after every restart."]);
});
