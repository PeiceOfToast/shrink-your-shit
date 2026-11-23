import { test } from "node:test";
import assert from "node:assert/strict";
import { encoderChip, formatsChip, graphicsLine, systemLine, usageBars, workerBars } from "./workerCards.ts";

test("graphicsLine: a named card with memory", () => {
  assert.equal(graphicsLine([{ model: "NVIDIA", vram: "8.0 GB" }]), "NVIDIA · 8.0 GB");
});

test("graphicsLine: an empty model never leaves an empty part or a doubled separator", () => {
  const out = graphicsLine([{ model: "", vram: "8.0 GB" }]);
  assert.equal(out, "8.0 GB");
  assert.ok(!/·\s*·/.test(out));
  assert.equal(graphicsLine([{ model: "  ", vram: "" }]), "Unnamed");
  assert.equal(graphicsLine([]), "Not reported");
});

test("graphicsLine: a remote worker or an unreadable one sends no GPU list, so no card is claimed", () => {
  assert.equal(graphicsLine([]), "Not reported");
});

test("graphicsLine: two cards are joined, an empty one is dropped", () => {
  assert.equal(graphicsLine([{ model: "Intel", vram: "" }, { model: "", vram: "" }, { model: "AMD", vram: "8.0 GB" }]), "Intel, AMD · 8.0 GB");
});

test("systemLine: host, OS, arch and memory, only the parts the worker sent", () => {
  assert.equal(systemLine("box", "linux", "x86_64", "32 GB"), "box · Linux · x86_64 · 32 GB");
  assert.equal(systemLine(undefined, undefined, undefined, ""), "");
  assert.equal(systemLine(undefined, "darwin", undefined, ""), "macOS");
});

test("usageBars: a bar only when the value exists, rounded", () => {
  assert.deepEqual(usageBars(73.6, null), [{ label: "Processor", pct: 74 }]);
  assert.deepEqual(usageBars(null, 91.2), [{ label: "Graphics card", pct: 91 }]);
  assert.deepEqual(usageBars(null, null), []);
});

test("encoderChip: used first, works, and not found", () => {
  assert.deepEqual(encoderChip("Processor", true, true), { text: "Processor · works · used first", tone: "accent" });
  assert.deepEqual(encoderChip("HEVC", true, false), { text: "HEVC · works", tone: "ok" });
  assert.deepEqual(encoderChip("NVIDIA", false, false), { text: "NVIDIA · not found", tone: "muted" });
});

test("formatsChip: only formats that passed; empty when none", () => {
  assert.equal(formatsChip([["H.264", true], ["HEVC", true], ["AV1", false]]), "H.264 · HEVC");
  assert.equal(formatsChip([["H.264", false]]), "");
});

test("workerBars: a 0 with no listed card is no GPU reading, so no Graphics bar", () => {
  assert.deepEqual(workerBars(30, [], [{ utilPercent: 0 }]), [{ label: "Processor", pct: 30 }]);
});

test("workerBars: a 0 on a listed card is a reading, so the bar shows", () => {
  assert.deepEqual(workerBars(30, [{}], [{ utilPercent: 0 }]), [{ label: "Processor", pct: 30 }, { label: "Graphics card", pct: 0 }]);
});

test("workerBars: a number above 0 shows the bar with no card listed (M5a fix 1)", () => {
  assert.deepEqual(workerBars(30, [], [{ utilPercent: 40 }]), [{ label: "Processor", pct: 30 }, { label: "Graphics card", pct: 40 }]);
});

test("workerBars: no number, no Graphics bar, even with a card listed", () => {
  assert.deepEqual(workerBars(30, [{}], [{ utilPercent: null }]), [{ label: "Processor", pct: 30 }]);
  assert.deepEqual(workerBars(30, [], []), [{ label: "Processor", pct: 30 }]);
});
