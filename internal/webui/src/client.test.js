import test from "node:test";
import assert from "node:assert/strict";
import { startClient } from "./client.js";

class Node {
  constructor(tag = "div") {
    this.tag = tag;
    this.children = [];
    this.listeners = {};
    this.dataset = {};
    this.value = "";
    this.checked = false;
    this.disabled = false;
    this.hidden = false;
    this.textContent = "";
    this.src = "";
    this.open = false;
    this.rect = { top: 0, bottom: 0, height: 0 };
  }
  append(...nodes) {
    this.children.push(...nodes);
    if (this.tag === "select" && !this.value && nodes[0])
      this.value = nodes[0].value;
  }
  replaceChildren(...nodes) {
    this.children = [...nodes];
    this.textContent = "";
    if (this.tag === "select") this.value = nodes[0]?.value || "";
  }
  setAttribute(name, value) {
    this[name] = String(value);
  }
  addEventListener(type, fn) {
    (this.listeners[type] ??= []).push(fn);
  }
  emit(type, event = {}) {
    const emitted = {
      target: this,
      data: this.data,
      preventDefault() {
        this.defaultPrevented = true;
      },
      ...event,
    };
    for (const fn of this.listeners[type] || []) fn(emitted);
    return emitted;
  }
  remove() {
    this.removed = true;
  }
  removeAttribute(name) {
    this[name] = "";
  }
  showModal() {
    this.open = true;
  }
  close() {
    this.open = false;
  }
  contains(node) {
    return (
      this === node || this.children.some((child) => child.contains?.(node))
    );
  }
  focus() {
    this.focused = true;
  }
  scrollIntoView(options) {
    this.scrolledIntoView = options;
  }
  getBoundingClientRect() {
    return this.rect;
  }
  setPointerCapture(id) {
    this.capturedPointer = id;
  }
  hasPointerCapture(id) {
    return this.capturedPointer === id;
  }
  releasePointerCapture(id) {
    if (this.capturedPointer === id) this.capturedPointer = undefined;
  }
  scrollBy(options) {
    this.scrolledBy = options;
  }
}
class FakeSocket extends Node {
  static OPEN = 1;
  static instances = [];
  constructor(url) {
    super("ws");
    this.url = url;
    this.readyState = 1;
    this.sent = [];
    FakeSocket.instances.push(this);
  }
  send(data) {
    this.sent.push(JSON.parse(data));
  }
  message(value) {
    this.data = JSON.stringify(value);
    this.emit("message");
  }
  close() {
    this.readyState = 3;
    this.emit("close");
  }
}
function fixture() {
  FakeSocket.instances = [];
  const ids = {},
    commands = [];
  for (const id of [
    "status",
    "connection-dot",
    "device-picker",
    "device-trigger",
    "devices",
    "roots",
    "library",
    "queue",
    "toast",
    "pending",
    "playback-state",
    "now-playing-title",
    "time",
    "seek",
    "volume-down",
    "volume-up",
    "mute",
    "transcode",
    "media-selected",
    "subtitle-selected",
    "subtitle-clear",
    "subtitle-selection",
    "selection-status",
    "artwork",
    "artwork-placeholder",
    "artwork-modal",
    "artwork-modal-image",
    "artwork-modal-title",
    "artwork-modal-close",
    "loop",
    "autoplay",
    "same-type",
    "gapless",
    "image-duration",
    "refresh",
    "queue-count",
    "queue-clear",
    "breadcrumbs",
    "folder-up",
    "add-visible",
    "add-visible-count",
    "library-filter",
    "play-toggle",
    "stop-button",
    "theme-toggle",
    "back-to-top",
  ])
    ids[id] = new Node();
  ids.roots.tag = "select";
  ids["play-toggle"].dataset.command = "player.play";
  ids["stop-button"].dataset.command = "player.stop";
  commands.push(ids["play-toggle"], ids["stop-button"]);
  const document = {
    listeners: {},
    documentElement: new Node("html"),
    querySelector: (selector) => ids[selector.slice(1)],
    querySelectorAll: (selector) =>
      selector === "[data-command]" ? commands : [],
    createElement: (tag) => new Node(tag),
    createElementNS: (_, tag) => new Node(tag),
    addEventListener(type, fn) {
      (this.listeners[type] ??= []).push(fn);
    },
    emit(type, event) {
      for (const fn of this.listeners[type] || []) fn(event);
    },
  };
  const window = {
    scrollY: 0,
    listeners: {},
    addEventListener(type, fn) {
      (this.listeners[type] ??= []).push(fn);
    },
    emit(type, event = {}) {
      for (const fn of this.listeners[type] || []) fn(event);
    },
    scrollTo(options) {
      this.scrolledTo = options;
    },
  };
  const snapshot = {
    revision: 3,
    devices: [
      {
        id: "dev-1",
        label: "<Kitchen>",
        protocol: "Chromecast",
        capabilities: ["audio_only"],
      },
      { id: "dev-2", label: "Living room", protocol: "DLNA" },
    ],
    selected_device_id: "dev-1",
    selected_media: false,
    selected_subtitle: false,
    queue: [],
    transcode: false,
    has_session: false,
    playback_state: "STOPPED",
    position: 0,
    duration: 0,
    volume: 25,
    muted: false,
    policy: {
      LoopSelected: false,
      AutoPlayNext: false,
      AutoPlaySameType: false,
      GaplessEnabled: false,
      ImageDurationSeconds: 10,
    },
  };
  const fetch = async (url) => ({
    ok: true,
    json: async () =>
      url === "/api/bootstrap"
        ? {
            protocol_version: 1,
            revision: 3,
            features: { transcode: true },
            snapshot,
            roots: [{ id: "root-1", name: "Media" }],
          }
        : {
            entries: [
              {
                id: "media-1",
                name: "<movie>.mp4",
                kind: "file",
                media_kind: "video",
                thumbnail_url: "/api/thumbnail?entry_id=media-1",
                artwork_url: "/api/media-artwork?entry_id=media-1",
              },
              { id: "sub-1", name: "captions.srt", kind: "file" },
            ],
          },
  });
  const store = () => {
    const data = new Map();
    return {
      getItem: (key) => data.get(key) || null,
      setItem: (key, value) => data.set(key, value),
      removeItem: (key) => data.delete(key),
    };
  };
  const mql = {
    matches: false,
    listeners: [],
    addEventListener(type, fn) {
      this.listeners.push(fn);
    },
    emit() {
      for (const fn of this.listeners) fn();
    },
  };
  const timers = [];
  const env = {
    document,
    window,
    fetch,
    WebSocket: FakeSocket,
    location: {
      protocol: "http:",
      host: "test",
      reload() {
        this.reloaded = true;
      },
    },
    sessionStorage: store(),
    localStorage: store(),
    matchMedia: () => mql,
    setTimeout: (fn) => (timers.push(fn), timers.length),
    clearTimeout() {},
  };
  return { ids, commands, document, window, env, timers, mql };
}
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

test("disables transcoding when FFmpeg is unavailable", async () => {
  const { ids, env } = fixture(),
    bootstrapFetch = env.fetch;
  env.fetch = async (url) => {
    const response = await bootstrapFetch(url),
      body = await response.json();
    return url === "/api/bootstrap"
      ? {
          ok: true,
          json: async () => ({ ...body, features: { transcode: false } }),
        }
      : response;
  };
  startClient(env);
  await settle();
  FakeSocket.instances[0].emit("open");
  assert.equal(ids.transcode.disabled, true);
  assert.equal(ids.transcode.title, "FFmpeg unavailable");
});

test("renders safe labels and sends playlist/play payloads", async () => {
  const { ids, document, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  assert.ok(ws);
  assert.equal(ids["device-trigger"].children[0].textContent, "<Kitchen>");
  assert.equal(
    ids["device-trigger"].children[1].children[0].textContent,
    "Chromecast",
  );
  assert.equal(
    ids["device-trigger"].children[1].children[1].textContent,
    "Audio only",
  );
  assert.equal(ids.devices.hidden, true);
  ids["device-trigger"].emit("click");
  document.emit("click", {
    composedPath: () => [ids["device-trigger"], ids["device-picker"], document],
  });
  assert.equal(ids.devices.hidden, false);
  assert.equal(ids.devices.children[0].dataset.selected, "true");
  assert.equal(
    ids.devices.children[1].children[1].children[0].textContent,
    "DLNA",
  );
  assert.equal(
    ids.library.children[0].children[0].children[1].children[0].textContent,
    "<movie>.mp4",
  );
  assert.equal(
    ids.library.children[0].children[0].children[1].children[1].textContent,
    "Video · MP4",
  );
  assert.equal(
    ids.library.children[1].children[0].children[1].children[1].textContent,
    "Subtitle · SRT",
  );
  assert.equal(ids["subtitle-selection"].hidden, true);
  assert.equal(ids["selection-status"].dataset.hasDetails, "false");
  ids.devices.children[1].emit("click");
  assert.equal(ws.sent[0].type, "devices.select");
  assert.deepEqual(ws.sent[0].payload, {
    device_id: "dev-2",
    expected_revision: 3,
  });
  assert.equal(ids.devices.hidden, true);
  assert.equal(
    ids.library.children[0].children[1].children[0].dataset.icon,
    "play",
  );
  assert.equal(
    ids.library.children[0].children[1].children[0].ariaLabel,
    "Play <movie>.mp4",
  );
  assert.equal(
    ids.library.children[0].children[1].children[1].dataset.icon,
    "list-plus",
  );
  ids.library.children[0].children[1].children[1].emit("click");
  assert.equal(ws.sent[1].type, "queue.add");
  assert.deepEqual(ws.sent[1].payload, {
    root_id: "root-1",
    entry_id: "media-1",
    expected_revision: 3,
  });
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: ws.sent[1].id,
    payload: { revision: 4 },
  });
  ids.library.children[0].children[1].children[0].emit("click");
  assert.equal(ws.sent[2].type, "library.play");
  assert.deepEqual(ws.sent[2].payload, {
    root_id: "root-1",
    entry_id: "media-1",
    expected_revision: 4,
  });
  assert.equal(ids.library.children[0].dataset.selected, "true");
  assert.equal(ids.library.children[0].ariaCurrent, "true");
  assert.equal(ids["subtitle-selection"].hidden, true);
  assert.equal(ids["selection-status"].dataset.hasDetails, "false");
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: ws.sent[2].id,
    payload: { revision: 5 },
  });
  ws.message({
    protocol_version: 1,
    type: "state.queue",
    payload: {
      revision: 4,
      queue: [
        {
          id: "stable-q",
          name: "<queued>",
          kind: "video",
          selected: true,
          active: false,
        },
      ],
    },
  });
  assert.equal(
    ids.queue.children[0].children[1].children[0].textContent,
    "<queued>",
  );
  assert.equal(ids.queue.children[0].children[2].children.length, 3);
  assert.equal(
    ids.queue.children[0].children[2].children[1].dataset.icon,
    "grip-vertical",
  );
  ids.queue.children[0].children[2].children[0].emit("click");
  assert.equal(ws.sent[3].type, "player.play");
  assert.equal(ws.sent[3].payload.item_id, "stable-q");
});

test("merges partial playback/policy, clears pending, reports errors and shutdown", async () => {
  const { ids, env, timers } = fixture();
  const client = startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  assert.equal(ids.mute.dataset.icon, "volume-x");
  assert.equal(ids.mute.ariaLabel, "Mute");
  assert.equal(ids.mute.ariaPressed, "false");
  ws.message({ protocol_version: 1, type: "pending", id: "7", payload: {} });
  assert.equal(ids.pending.textContent, "1 working");
  ws.message({
    protocol_version: 1,
    type: "state.playback",
    payload: {
      revision: 8,
      state: "PLAYING",
      position: 61,
      duration: 125,
      volume: 44,
      muted: true,
      has_session: true,
    },
  });
  assert.equal(ids.time.textContent, "1:01 / 2:05");
  assert.equal(ids.seek.value, "61");
  assert.equal(ids.mute.dataset.icon, "volume-x");
  assert.equal(ids.mute.ariaLabel, "Unmute");
  assert.equal(ids["volume-down"].disabled, false);
  assert.equal(ids["volume-up"].disabled, false);
  ws.message({
    protocol_version: 1,
    type: "state.playback",
    payload: { revision: 9, volume: 100 },
  });
  assert.equal(ids["volume-down"].disabled, false);
  assert.equal(ids["volume-up"].disabled, false);
  ws.message({
    protocol_version: 1,
    type: "state.playback",
    payload: { revision: 10, volume: 0 },
  });
  assert.equal(ids["volume-down"].disabled, false);
  assert.equal(ids["volume-up"].disabled, false);
  ws.message({
    protocol_version: 1,
    type: "state.policy",
    payload: {
      revision: 11,
      policy: {
        LoopSelected: false,
        AutoPlayNext: true,
        AutoPlaySameType: true,
        GaplessEnabled: true,
        ImageDurationSeconds: 7,
      },
    },
  });
  assert.equal(ids.autoplay.checked, true);
  assert.equal(ids["same-type"].disabled, false);
  assert.equal(ids.gapless.checked, true);
  assert.equal(ids.gapless.disabled, true);
  ws.message({
    protocol_version: 1,
    type: "state.selection",
    payload: { device_id: "dev-2" },
  });
  assert.equal(ids.gapless.disabled, false);
  ws.message({
    protocol_version: 1,
    type: "error",
    id: "7",
    payload: { code: "invalid", message: "Try again" },
  });
  assert.equal(client.pending.size, 0);
  assert.equal(ids.toast.children[0].textContent, "Try again");
  client.send("player.stop");
  assert.equal(client.pending.size, 1);
  ws.close();
  assert.equal(client.pending.size, 0);
  timers.at(-1)();
  await settle();
  const ws2 = FakeSocket.instances[1];
  assert.ok(ws2);
  ws2.message({ protocol_version: 1, type: "server.shutdown", payload: {} });
  assert.equal(ids.status.textContent, "Server stopped");
});

test("reconnects after a server restart when assets are unchanged", async () => {
  const { ids, env, timers } = fixture();
  const bootstrapFetch = env.fetch;
  env.fetch = async (url) => {
    if (url !== "/api/bootstrap") return bootstrapFetch(url);
    const response = await bootstrapFetch(url),
      body = await response.json();
    return { ok: true, json: async () => ({ ...body, assets_hash: "abc123" }) };
  };
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  ws.message({ protocol_version: 1, type: "server.shutdown", payload: {} });
  assert.equal(ids.status.textContent, "Server stopped");
  ws.close();
  assert.equal(ids.status.textContent, "Server stopped");
  timers.at(-1)();
  await settle();
  const ws2 = FakeSocket.instances[1];
  assert.ok(ws2, "socket reopens after restart");
  assert.equal(env.location.reloaded, undefined);
  ws2.emit("open");
  assert.equal(ids.status.textContent, "Connected");
});

test("reloads the page when assets change across a server restart", async () => {
  const { env, timers } = fixture();
  const bootstrapFetch = env.fetch;
  let hash = "abc123";
  env.fetch = async (url) => {
    if (url !== "/api/bootstrap") return bootstrapFetch(url);
    const response = await bootstrapFetch(url),
      body = await response.json();
    return { ok: true, json: async () => ({ ...body, assets_hash: hash }) };
  };
  startClient(env);
  await settle();
  hash = "def456";
  FakeSocket.instances[0].close();
  timers.at(-1)();
  await settle();
  assert.equal(env.location.reloaded, true);
  assert.equal(FakeSocket.instances.length, 1);
});

test("re-resolves stale library and root IDs after a server restart", async () => {
  const { ids, env, timers } = fixture();
  const bootstrapFetch = env.fetch;
  let generation = 1;
  env.fetch = async (url) => {
    if (url === "/api/bootstrap") {
      const response = await bootstrapFetch(url),
        body = await response.json();
      return {
        ok: true,
        json: async () => ({
          ...body,
          instance_id: `gen-${generation}`,
          roots: [{ id: `root-gen${generation}`, name: "Media" }],
        }),
      };
    }
    const params = new URL(`http://host${url}`).searchParams,
      parent = params.get("parent_id") || "";
    assert.equal(params.get("root_id"), `root-gen${generation}`);
    return {
      ok: true,
      json: async () => ({
        entries: parent
          ? [
              {
                id: `episode-gen${generation}`,
                name: "Episode 1.mkv",
                kind: "file",
                media_kind: "video",
              },
            ]
          : [
              {
                id: `anime-gen${generation}`,
                name: "Anime",
                kind: "directory",
              },
            ],
      }),
    };
  };
  startClient(env);
  await settle();
  ids.library.children[0].children[1].children[0].emit("click");
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  generation = 2;
  ws.message({ protocol_version: 1, type: "server.shutdown", payload: {} });
  ws.close();
  timers.at(-1)();
  await settle();
  const ws2 = FakeSocket.instances[1];
  assert.ok(ws2, "socket reopens after restart");
  ws2.emit("open");
  assert.equal(ids.breadcrumbs.children.at(-1).textContent, "Anime");
  ids.library.children[0].children[1].children[1].emit("click");
  assert.equal(ws2.sent.at(-1).type, "queue.add");
  assert.deepEqual(ws2.sent.at(-1).payload, {
    root_id: "root-gen2",
    entry_id: "episode-gen2",
    expected_revision: 3,
  });
});

test("keeps probing while the server is down, then reconnects", async () => {
  const { ids, env, timers } = fixture();
  const bootstrapFetch = env.fetch;
  let down = false;
  env.fetch = async (url) => {
    if (down) throw new Error("connection refused");
    return bootstrapFetch(url);
  };
  startClient(env);
  await settle();
  down = true;
  FakeSocket.instances[0].close();
  assert.equal(ids.status.textContent, "Reconnecting…");
  timers.at(-1)();
  await settle();
  assert.equal(FakeSocket.instances.length, 1);
  down = false;
  timers.at(-1)();
  await settle();
  assert.ok(FakeSocket.instances[1], "socket reopens once the server returns");
});

test("control interactions emit typed payloads and subtitle selection", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ids.library.children[1].children[1].children[0].emit("click");
  assert.equal(ids["subtitle-selected"].textContent, "captions.srt");
  assert.equal(ids["subtitle-clear"].hidden, false);
  assert.equal(ids["subtitle-selection"].hidden, false);
  assert.equal(ids["selection-status"].dataset.hasDetails, "true");
  assert.equal(ids["selection-status"].open, true);
  ids.seek.value = "42";
  ids.seek.emit("change");
  ids["volume-up"].emit("click");
  ids["volume-down"].emit("click");
  ids.mute.emit("click");
  ids.transcode.checked = true;
  ids.transcode.emit("change");
  ids.autoplay.checked = true;
  ids["same-type"].checked = true;
  ids.gapless.checked = true;
  ids["image-duration"].value = "12";
  ids["same-type"].emit("change");
  assert.deepEqual(
    ws.sent.map((message) => [message.type, message.payload]),
    [
      [
        "library.select_subtitle",
        { root_id: "root-1", entry_id: "sub-1", expected_revision: 3 },
      ],
      ["player.seek", { seconds: 42, expected_revision: 3 }],
      ["player.volume", { delta: 1, expected_revision: 3 }],
      ["player.volume", { delta: -1, expected_revision: 3 }],
      ["player.mute", { muted: true, expected_revision: 3 }],
      ["player.transcode", { enabled: true, expected_revision: 3 }],
      [
        "playback.policy",
        {
          policy: {
            LoopSelected: false,
            AutoPlayNext: true,
            AutoPlaySameType: true,
            GaplessEnabled: true,
            ImageDurationSeconds: 12,
          },
          expected_revision: 3,
        },
      ],
    ],
  );
});

test("position-only playback update redraws progress only", async () => {
  const { ids, env } = fixture();
  const client = startClient(env);
  await settle();
  ids["playback-state"].textContent = "unchanged";
  ids["media-selected"].textContent = "unchanged";
  client.handle({
    protocol_version: 1,
    type: "state.playback",
    payload: { revision: 4, position: 5, duration: 60 },
  });
  assert.equal(ids.time.textContent, "0:05 / 1:00");
  assert.equal(ids.seek.value, "5");
  assert.equal(ids["playback-state"].textContent, "unchanged");
  assert.equal(ids["media-selected"].textContent, "unchanged");
});

test("shows elapsed position when duration is unavailable", async () => {
  const { ids, env } = fixture();
  const client = startClient(env);
  await settle();
  client.handle({
    protocol_version: 1,
    type: "state.playback",
    payload: {
      revision: 4,
      state: "PLAYING",
      position: 65,
      duration: 0,
      has_session: true,
    },
  });
  assert.equal(ids.time.textContent, "1:05 / 0:00");
  assert.equal(ids.seek.value, "0");
  assert.equal(ids.seek.disabled, true);
});

test("previews seek target while dragging", async () => {
  const { ids, env } = fixture();
  const client = startClient(env);
  await settle();
  FakeSocket.instances[0].emit("open");
  client.handle({
    protocol_version: 1,
    type: "state.playback",
    payload: {
      revision: 4,
      state: "PLAYING",
      position: 61,
      duration: 6176,
      has_session: true,
    },
  });

  ids.seek.value = "3723";
  ids.seek.emit("input");
  assert.equal(ids.time.textContent, "1:02:03 / 1:42:56");

  client.handle({
    protocol_version: 1,
    type: "state.playback",
    payload: { revision: 5, position: 62 },
  });
  assert.equal(ids.time.textContent, "1:02:03 / 1:42:56");
  assert.equal(ids.seek.value, "3723");

  ids.seek.emit("change");
  assert.deepEqual(FakeSocket.instances[0].sent.at(-1), {
    protocol_version: 1,
    type: "player.seek",
    id: "1",
    payload: { seconds: 3723, expected_revision: 5 },
  });
});

test("player pending state preserves device picker DOM", async () => {
  const { ids, env } = fixture();
  const client = startClient(env);
  await settle();
  FakeSocket.instances[0].emit("open");
  const selectedDevice = ids["device-trigger"].children[0];
  client.send("player.seek", { seconds: 5 });
  assert.equal(ids["device-trigger"].children[0], selectedDevice);
});

test("protocol mismatch reloads at most once", async () => {
  const first = fixture();
  startClient(first.env);
  await settle();
  FakeSocket.instances[0].message({
    protocol_version: 2,
    type: "state.snapshot",
    payload: {},
  });
  assert.equal(first.env.location.reloaded, true);
  const second = fixture();
  second.env.sessionStorage.setItem("go2tv-protocol-reload", "1");
  startClient(second.env);
  await settle();
  FakeSocket.instances[0].message({
    protocol_version: 2,
    type: "state.snapshot",
    payload: {},
  });
  assert.equal(second.env.location.reloaded, undefined);
  assert.equal(second.ids.status.textContent, "Incompatible server");
});

test("shows selected names and silently retries revision conflicts", async () => {
  const { ids, env } = fixture();
  const client = startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ids.library.children[0].children[1].children[0].emit("click");
  assert.equal(ids["media-selected"].textContent, "<movie>.mp4");
  ws.message({
    protocol_version: 1,
    type: "state.queue",
    payload: {
      revision: 4,
      queue: [
        {
          id: "selected-q",
          name: "<movie>.mp4",
          kind: "video",
          selected: true,
          active: false,
        },
      ],
    },
  });
  assert.deepEqual(ids.queue.children[0].scrolledIntoView, {
    behavior: "smooth",
    block: "nearest",
  });
  ws.message({
    protocol_version: 1,
    type: "state.selection",
    payload: {
      revision: 4,
      media: true,
      media_name: "long song.flac",
      media_type: "audio",
      subtitle: true,
      subtitle_name: "captions.srt",
    },
  });
  assert.equal(ids["media-selected"].textContent, "long song.flac");
  assert.equal(ids["subtitle-selected"].textContent, "captions.srt");
  assert.equal(ids["subtitle-clear"].hidden, false);
  assert.equal(ids["subtitle-selection"].hidden, false);
  ids["subtitle-clear"].emit("click");
  assert.equal(ws.sent.at(-1).type, "library.clear_subtitle");
  assert.deepEqual(ws.sent.at(-1).payload, { expected_revision: 4 });
  ws.message({
    protocol_version: 1,
    type: "state.selection",
    payload: {
      revision: 5,
      subtitle: false,
      subtitle_name: "",
      media_type: "video",
    },
  });
  assert.equal(ids["subtitle-selected"].textContent, "None");
  assert.equal(ids["subtitle-clear"].hidden, true);
  assert.equal(ids["subtitle-selection"].hidden, true);
  assert.equal(ids["selection-status"].dataset.hasDetails, "false");
  const first = client.send("player.seek", { seconds: 12 });
  ws.message({
    protocol_version: 1,
    type: "error",
    id: first,
    payload: { code: "conflict", message: "state changed", revision: 5 },
  });
  assert.equal(ws.sent.at(-1).type, "player.seek");
  assert.deepEqual(ws.sent.at(-1).payload, {
    seconds: 12,
    expected_revision: 5,
  });
  assert.equal(ids.toast.children.length, 0);
  const retry = ws.sent.at(-1).id;
  ws.message({
    protocol_version: 1,
    type: "error",
    id: retry,
    payload: { code: "conflict", message: "state changed", revision: 6 },
  });
  assert.deepEqual(ws.sent.at(-1).payload, {
    seconds: 12,
    expected_revision: 6,
  });
  assert.equal(ids.toast.children.length, 0);
  const finalRetry = ws.sent.at(-1).id;
  ws.message({
    protocol_version: 1,
    type: "error",
    id: finalRetry,
    payload: { code: "conflict", message: "state changed", revision: 7 },
  });
  assert.equal(
    ids.toast.children[0].textContent,
    "The app kept changing. Please try that action again.",
  );
});

test("loads browser thumbnails, opens artwork modal and updates player artwork", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const row = ids.library.children[0],
    thumbnail = row.children[0].children[0];
  assert.equal(thumbnail.children[0].src, "/api/thumbnail?entry_id=media-1");
  thumbnail.children[0].emit("load");
  assert.equal(thumbnail.children[0].hidden, false);
  thumbnail.emit("click");
  assert.equal(ids["artwork-modal"].open, true);
  assert.equal(
    ids["artwork-modal-image"].src,
    "/api/media-artwork?entry_id=media-1",
  );
  assert.equal(ids["artwork-modal-title"].textContent, "<movie>.mp4");
  ids["artwork-modal-close"].emit("click");
  assert.equal(ids["artwork-modal"].open, false);
  row.children[1].children[0].emit("click");
  assert.equal(ids.artwork.src, "");
  assert.equal(ids.artwork.hidden, true);
  FakeSocket.instances[0].message({
    protocol_version: 1,
    type: "state.selection",
    payload: { revision: 4, artwork_id: "cover-id", media_type: "video" },
  });
  assert.equal(ids.artwork.src, "/api/artwork/cover-id.jpg");
  assert.equal(ids["artwork-placeholder"].hidden, true);
  FakeSocket.instances[0].message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: { revision: 5, artwork_id: "" },
  });
  assert.equal(ids.artwork.src, "");
  assert.equal(ids.artwork.hidden, true);
  assert.equal(ids["artwork-placeholder"].hidden, false);
});

test("queue and primary transport follow loading and active state", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 4,
      selected_media: true,
      selected_media_name: "next.flac",
      active_media_name: "current.flac",
      has_session: true,
      playback_state: "PLAYING",
      queue: [
        {
          id: "q1",
          name: "current.flac",
          kind: "audio",
          selected: true,
          active: true,
        },
      ],
    },
  });
  assert.equal(ids["now-playing-title"].textContent, "current.flac");
  assert.equal(ids["play-toggle"].dataset.icon, "pause");
  assert.equal(ids["play-toggle"].ariaLabel, "Pause");
  assert.equal(
    ids.queue.children[0].children[2].children[0].dataset.icon,
    "pause",
  );
  assert.equal(ids.queue.children[0].dataset.current, "true");
  assert.equal(ids.queue.children[0].dataset.active, undefined);
  assert.equal(ids.queue.children[0].children[2].children[2].disabled, true);
  assert.equal(
    ids.queue.children[0].children[2].children[2].title,
    "Cannot remove current item",
  );
  ids.queue.children[0].children[2].children[0].emit("click");
  const pauseRequest = ws.sent.at(-1);
  assert.equal(pauseRequest.type, "player.pause");
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: pauseRequest.id,
    payload: { revision: 5 },
  });
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 5,
      selected_media: true,
      selected_media_name: "current.flac",
      active_media_name: "current.flac",
      has_session: true,
      playback_state: "PAUSED",
      queue: [
        {
          id: "q1",
          name: "current.flac",
          kind: "audio",
          selected: true,
          active: true,
        },
      ],
    },
  });
  assert.equal(ids["play-toggle"].dataset.icon, "play");
  assert.equal(ids["play-toggle"].ariaLabel, "Resume");
  assert.equal(
    ids.queue.children[0].children[2].children[0].dataset.icon,
    "play",
  );
  assert.equal(
    ids.queue.children[0].children[2].children[0].ariaLabel,
    "Resume current.flac",
  );
  assert.equal(ids.queue.children[0].children[2].children[2].disabled, true);
  assert.equal(
    ids.queue.children[0].children[2].children[2].title,
    "Cannot remove current item",
  );
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 6,
      selected_media: true,
      selected_media_name: "next.flac",
      active_media_name: "current.flac",
      has_session: true,
      playback_state: "LOADING",
      queue: [
        {
          id: "q1",
          name: "current.flac",
          kind: "audio",
          selected: false,
          active: true,
        },
        {
          id: "q2",
          name: "next.flac",
          kind: "audio",
          selected: true,
          active: false,
        },
      ],
    },
  });
  assert.equal(ids["now-playing-title"].textContent, "next.flac");
  assert.equal(
    ids.queue.children[1].children[2].children[0].dataset.icon,
    "loader-circle",
  );
  assert.equal(
    ids.queue.children[1].children[2].children[0].ariaLabel,
    "Starting next.flac",
  );
  assert.equal(
    ids.queue.children[1].children[1].children[0].textContent,
    "next.flac",
  );
});

test("playing another playlist item replaces active playback", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 4,
      has_session: true,
      playback_state: "PLAYING",
      queue: [
        {
          id: "q1",
          name: "one.mp3",
          kind: "audio",
          selected: true,
          active: true,
        },
        {
          id: "q2",
          name: "two.mp3",
          kind: "audio",
          selected: false,
          active: false,
        },
      ],
    },
  });
  ids.queue.children[1].children[2].children[0].emit("click");
  assert.equal(ws.sent.at(-1).type, "player.play");
  assert.deepEqual(ws.sent.at(-1).payload, {
    item_id: "q2",
    expected_revision: 4,
  });
});

test("playlist locks every row during mutations and transitions", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  ws.message({
    protocol_version: 1,
    type: "state.queue",
    payload: {
      revision: 4,
      queue: [
        {
          id: "q1",
          name: "one.mp3",
          kind: "audio",
          selected: true,
          active: false,
        },
        {
          id: "q2",
          name: "two.mp3",
          kind: "audio",
          selected: false,
          active: false,
        },
      ],
    },
  });
  assert.equal(ids.queue.children[0].children[2].children[2].disabled, false);
  assert.equal(ids.queue.children[0].children[2].children[2].title, "Remove");
  assert.equal(ids.queue.children[1].children[2].children[2].disabled, false);
  assert.equal(ids.queue.children[1].children[2].children[0].disabled, false);
  ids.queue.children[0].children[2].children[0].emit("click");
  for (const row of ids.queue.children)
    for (const control of row.children[2].children)
      assert.equal(control.disabled, true);
  const request = ws.sent.at(-1).id;
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: request,
    payload: { revision: 5 },
  });
  assert.equal(ids.queue.children[1].children[2].children[0].disabled, false);
  ws.message({
    protocol_version: 1,
    type: "state.playback",
    payload: { revision: 6, state: "LOADING", has_session: true },
  });
  for (const row of ids.queue.children)
    for (const control of row.children[2].children)
      assert.equal(control.disabled, true);
});

test("dragging to playlist bottom marks the final bottom edge", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  const items = [
    { id: "q1", name: "one.mp3", kind: "audio", selected: true, active: false },
    {
      id: "q2",
      name: "two.mp3",
      kind: "audio",
      selected: false,
      active: false,
    },
    {
      id: "q3",
      name: "three.mp3",
      kind: "audio",
      selected: false,
      active: false,
    },
  ];
  ws.message({
    protocol_version: 1,
    type: "state.queue",
    payload: { revision: 4, queue: items },
  });
  ids.queue.rect = { top: 0, bottom: 300, height: 300 };
  for (const [index, row] of ids.queue.children.entries())
    row.rect = { top: index * 100, bottom: (index + 1) * 100, height: 100 };
  const handle = ids.queue.children[0].children[2].children[1];
  assert.equal(handle.dataset.icon, "grip-vertical");
  assert.equal(handle.ariaLabel, "Reorder one.mp3. Drag or use arrow keys");
  handle.emit("pointerdown", {
    pointerId: 7,
    pointerType: "touch",
    button: 0,
    clientY: 50,
  });
  assert.equal(handle.capturedPointer, 7);
  assert.equal(ids.queue.children[0].dataset.dragging, "true");
  handle.emit("pointermove", {
    pointerId: 7,
    pointerType: "touch",
    clientY: 320,
  });
  assert.equal(ids.queue.children[2].dataset.dropPosition, "after");
  assert.equal(ids.queue.children[0].dataset.dropPosition, undefined);
  handle.emit("pointerup", {
    pointerId: 7,
    pointerType: "touch",
    clientY: 320,
  });
  assert.equal(ws.sent.length, 1);
  assert.equal(ws.sent[0].type, "queue.move");
  assert.deepEqual(ws.sent[0].payload, {
    item_id: "q1",
    delta: 2,
    expected_revision: 4,
  });
  assert.equal(ids.queue.dataset.dragging, undefined);
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: ws.sent[0].id,
    payload: { revision: 5 },
  });
  ws.message({
    protocol_version: 1,
    type: "state.queue",
    payload: { revision: 5, queue: [items[1], items[2], items[0]] },
  });
  ids.queue.children[2].children[2].children[1].emit("keydown", {
    key: "ArrowUp",
  });
  assert.equal(ws.sent.at(-1).type, "queue.move");
  assert.deepEqual(ws.sent.at(-1).payload, {
    item_id: "q1",
    delta: -1,
    expected_revision: 5,
  });
});

test("load more survives filtering and appends the next page", async () => {
  const { ids, env } = fixture();
  const bootstrapFetch = env.fetch;
  env.fetch = async (url) => {
    if (!url.startsWith("/api/library")) return bootstrapFetch(url);
    const cursor =
      new URL(`http://host${url}`).searchParams.get("cursor") || "";
    return {
      ok: true,
      json: async () =>
        cursor
          ? {
              entries: [
                {
                  id: "media-2",
                  name: "second.mp4",
                  kind: "file",
                  media_kind: "video",
                },
              ],
            }
          : {
              entries: [
                {
                  id: "media-1",
                  name: "first.mp4",
                  kind: "file",
                  media_kind: "video",
                },
              ],
              cursor: "next",
            },
    };
  };
  startClient(env);
  await settle();
  const loadMore = () =>
    ids.library.children.find((node) => node.className === "browser-nav");
  assert.ok(loadMore(), "load more visible after first page");
  ids["library-filter"].value = "zzz";
  ids["library-filter"].emit("input");
  assert.equal(
    ids.library.children[0].textContent,
    "No matches in this folder.",
  );
  assert.ok(loadMore(), "load more visible while filter hides everything");
  ids["library-filter"].value = "";
  ids["library-filter"].emit("input");
  loadMore().children[0].emit("click");
  await settle();
  assert.equal(loadMore(), undefined);
  assert.deepEqual(
    ids.library.children.map(
      (row) => row.children[0]?.children[1]?.children[0]?.textContent,
    ),
    ["first.mp4", "second.mp4"],
  );
});

test("orders library entries by name across pages", async () => {
  const { ids, env } = fixture();
  const bootstrapFetch = env.fetch;
  env.fetch = async (url) => {
    if (!url.startsWith("/api/library")) return bootstrapFetch(url);
    const cursor =
      new URL(`http://host${url}`).searchParams.get("cursor") || "";
    return {
      ok: true,
      json: async () =>
        cursor
          ? {
              entries: [
                {
                  id: "m-4",
                  name: "Episode 2.mkv",
                  kind: "file",
                  media_kind: "video",
                },
              ],
            }
          : {
              entries: [
                {
                  id: "m-2",
                  name: "zebra.mp4",
                  kind: "file",
                  media_kind: "video",
                },
                { id: "dir-1", name: "Concerts", kind: "directory" },
                {
                  id: "m-3",
                  name: "Episode 10.mkv",
                  kind: "file",
                  media_kind: "video",
                },
                {
                  id: "m-1",
                  name: "alpha.mp3",
                  kind: "file",
                  media_kind: "audio",
                },
              ],
              cursor: "next",
            },
    };
  };
  startClient(env);
  await settle();
  const names = () =>
    ids.library.children
      .filter((row) => row.className === "library-row")
      .map((row) => row.children[0].children[1].children[0].textContent);
  assert.deepEqual(names(), [
    "alpha.mp3",
    "Concerts",
    "Episode 10.mkv",
    "zebra.mp4",
  ]);
  ids.library.children
    .find((node) => node.className === "browser-nav")
    .children[0].emit("click");
  await settle();
  assert.deepEqual(names(), [
    "alpha.mp3",
    "Concerts",
    "Episode 2.mkv",
    "Episode 10.mkv",
    "zebra.mp4",
  ]);
});

test("bulk add sends listed media in display order and reports counts", async () => {
  const { ids, env } = fixture();
  const bootstrapFetch = env.fetch;
  env.fetch = async (url) =>
    url.startsWith("/api/library")
      ? {
          ok: true,
          json: async () => ({
            entries: [
              { id: "id-b", name: "b.mp4", kind: "file", media_kind: "video" },
              { id: "dir-1", name: "a folder", kind: "directory" },
              { id: "sub-1", name: "a.srt", kind: "file" },
              {
                id: "id-c10",
                name: "c 10.mp4",
                kind: "file",
                media_kind: "video",
              },
              { id: "id-a", name: "a.mp4", kind: "file", media_kind: "video" },
              {
                id: "id-c2",
                name: "c 2.mp4",
                kind: "file",
                media_kind: "video",
              },
            ],
          }),
        }
      : bootstrapFetch(url);
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  assert.equal(ids["add-visible"].disabled, false);
  assert.equal(ids["add-visible-count"].hidden, false);
  assert.equal(ids["add-visible-count"].textContent, "4");
  assert.equal(ids["add-visible"].ariaLabel, "Add 4 listed files to playlist");
  ids["add-visible"].emit("click");
  assert.equal(ws.sent[0].type, "queue.add_many");
  assert.deepEqual(ws.sent[0].payload, {
    root_id: "root-1",
    entry_ids: ["id-a", "id-b", "id-c2", "id-c10"],
    expected_revision: 3,
  });
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: ws.sent[0].id,
    payload: { revision: 4, added: 3, duplicates: 1, dropped: 0, failed: 0 },
  });
  const toast = ids.toast.children.at(-1);
  assert.equal(
    toast.textContent,
    "Added 3 files to playlist; 1 already in playlist",
  );
  assert.equal(toast.dataset.level, "info");
  ids["library-filter"].value = "c ";
  ids["library-filter"].emit("input");
  assert.equal(ids["add-visible-count"].textContent, "2");
  ids["add-visible"].emit("click");
  assert.deepEqual(ws.sent[1].payload, {
    root_id: "root-1",
    entry_ids: ["id-c2", "id-c10"],
    expected_revision: 4,
  });
  ids["library-filter"].value = "zzz";
  ids["library-filter"].emit("input");
  assert.equal(ids["add-visible"].disabled, true);
  assert.equal(ids["add-visible-count"].hidden, true);
  ids["add-visible"].emit("click");
  assert.equal(ws.sent.length, 2);
});

test("bulk add truncates to the queue limit and reports unsent files", async () => {
  const { ids, env } = fixture();
  const bootstrapFetch = env.fetch;
  env.fetch = async (url) => {
    if (url === "/api/bootstrap") {
      const response = await bootstrapFetch(url),
        body = await response.json();
      return {
        ok: true,
        json: async () => ({ ...body, limits: { queue_items: 3 } }),
      };
    }
    return {
      ok: true,
      json: async () => ({
        entries: ["a", "b", "c", "d", "e"].map((name) => ({
          id: `id-${name}`,
          name: `${name}.mp4`,
          kind: "file",
          media_kind: "video",
        })),
      }),
    };
  };
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ids["add-visible"].emit("click");
  assert.deepEqual(ws.sent[0].payload, {
    root_id: "root-1",
    entry_ids: ["id-a", "id-b", "id-c"],
    expected_revision: 3,
  });
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: ws.sent[0].id,
    payload: { revision: 4, added: 2, duplicates: 0, dropped: 1, failed: 0 },
  });
  assert.equal(
    ids.toast.children.at(-1).textContent,
    "Added 2 files to playlist; 3 skipped (playlist full)",
  );
});

test("bulk add badge caps its display at 999+", async () => {
  const { ids, env } = fixture();
  const bootstrapFetch = env.fetch;
  env.fetch = async (url) =>
    url.startsWith("/api/library")
      ? {
          ok: true,
          json: async () => ({
            entries: Array.from({ length: 1200 }, (_, index) => ({
              id: `id-${index}`,
              name: `clip ${String(index).padStart(4, "0")}.mp4`,
              kind: "file",
              media_kind: "video",
            })),
          }),
        }
      : bootstrapFetch(url);
  startClient(env);
  await settle();
  assert.equal(ids["add-visible-count"].textContent, "999+");
  assert.equal(
    ids["add-visible"].ariaLabel,
    "Add 1200 listed files to playlist",
  );
  const ws = FakeSocket.instances[0];
  ids["add-visible"].emit("click");
  assert.equal(ws.sent[0].payload.entry_ids.length, 1000);
  assert.equal(ws.sent[0].payload.entry_ids[0], "id-0");
});

test("folder navigation exposes current location and one-level up", async () => {
  const { ids, env } = fixture(),
    bootstrapFetch = env.fetch,
    browsedParents = [];
  env.fetch = async (url) => {
    if (!url.startsWith("/api/library")) return bootstrapFetch(url);
    const parent =
      new URL(`http://host${url}`).searchParams.get("parent_id") || "";
    browsedParents.push(parent);
    return {
      ok: true,
      json: async () => ({
        entries: parent
          ? [
              {
                id: "episode-1",
                name: "Episode 1.mkv",
                kind: "file",
                media_kind: "video",
              },
            ]
          : [{ id: "anime", name: "Anime", kind: "directory" }],
      }),
    };
  };
  startClient(env);
  await settle();
  assert.equal(ids["folder-up"].hidden, true);
  assert.equal(ids.breadcrumbs.children[0].ariaCurrent, "page");
  ids.library.children[0].children[1].children[0].emit("click");
  await settle();
  assert.equal(ids["folder-up"].hidden, false);
  assert.equal(ids["folder-up"].ariaLabel, "Up to Library");
  assert.equal(ids.breadcrumbs.children.at(-1).textContent, "Anime");
  assert.equal(ids.breadcrumbs.children.at(-1).ariaCurrent, "page");
  ids["folder-up"].emit("click");
  await settle();
  assert.equal(ids["folder-up"].hidden, true);
  assert.equal(ids.breadcrumbs.children[0].ariaCurrent, "page");
  assert.deepEqual(browsedParents, ["", "anime", ""]);
});

test("stopped selected playlist item can be removed and clears player", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 4,
      selected_media: true,
      selected_media_name: "one.mp3",
      artwork_id: "cover",
      has_session: false,
      playback_state: "STOPPED",
      position: 54,
      duration: 144,
      queue: [
        {
          id: "q1",
          name: "one.mp3",
          kind: "audio",
          selected: true,
          active: false,
        },
        {
          id: "q2",
          name: "two.mp3",
          kind: "audio",
          selected: false,
          active: false,
        },
      ],
    },
  });
  const remove = ids.queue.children[0].children[2].children[2];
  assert.equal(remove.disabled, false);
  assert.equal(remove.title, "Remove");
  remove.emit("click");
  const request = ws.sent.at(-1);
  assert.equal(request.type, "queue.remove");
  assert.deepEqual(request.payload, { item_id: "q1", expected_revision: 4 });
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: request.id,
    payload: { revision: 5 },
  });
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 5,
      selected_media: false,
      artwork_id: "",
      has_session: false,
      playback_state: "STOPPED",
      position: 0,
      duration: 0,
      queue: [
        {
          id: "q2",
          name: "two.mp3",
          kind: "audio",
          selected: false,
          active: false,
        },
      ],
    },
  });
  assert.equal(ids["now-playing-title"].textContent, "Nothing playing");
  assert.equal(ids.time.textContent, "0:00 / 0:00");
  assert.equal(ids.seek.value, "0");
  assert.equal(ids.artwork.src, "");
  assert.equal(ids.artwork.hidden, true);
  assert.equal(ids["artwork-placeholder"].hidden, false);
});

test("clear queue button sends queue.clear and tracks queue state", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  assert.equal(ids["queue-clear"].disabled, true);
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 4,
      selected_media: true,
      selected_media_name: "one.mp3",
      active_media_name: "one.mp3",
      has_session: false,
      playback_state: "STOPPED",
      position: 54,
      duration: 144,
      queue: [
        {
          id: "q1",
          name: "one.mp3",
          kind: "audio",
          selected: true,
          active: false,
        },
      ],
    },
  });
  assert.equal(ids["now-playing-title"].textContent, "one.mp3");
  assert.equal(ids.time.textContent, "0:54 / 2:24");
  assert.equal(ids["queue-clear"].disabled, false);
  ids["queue-clear"].emit("click");
  assert.equal(ws.sent.at(-1).type, "queue.clear");
  assert.deepEqual(ws.sent.at(-1).payload, { expected_revision: 4 });
  assert.equal(ids["queue-clear"].disabled, true);
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: ws.sent.at(-1).id,
    payload: { revision: 5 },
  });
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 5,
      selected_media: false,
      has_session: false,
      playback_state: "STOPPED",
      position: 0,
      duration: 0,
      queue: [],
    },
  });
  assert.equal(ids["queue-clear"].disabled, true);
  assert.equal(ids["queue-count"].textContent, "0");
  assert.equal(
    ids.queue.children[0].textContent,
    "Playlist is empty. Add something from your library.",
  );
  assert.equal(ids["now-playing-title"].textContent, "Nothing playing");
  assert.equal(ids.time.textContent, "0:00 / 0:00");
  assert.equal(ids.seek.value, "0");
});

test("clear queue retains active item controls and artwork", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ws.emit("open");
  const active = {
    id: "q1",
    name: "one.mp3",
    kind: "audio",
    selected: true,
    active: true,
  };
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 4,
      selected_media: true,
      selected_media_name: "one.mp3",
      active_media_name: "one.mp3",
      artwork_id: "cover",
      has_session: true,
      playback_state: "PLAYING",
      queue: [
        active,
        {
          id: "q2",
          name: "two.mp3",
          kind: "audio",
          selected: false,
          active: false,
        },
      ],
    },
  });
  ids["queue-clear"].emit("click");
  const request = ws.sent.at(-1);
  assert.equal(request.type, "queue.clear");
  ws.message({
    protocol_version: 1,
    type: "ack",
    id: request.id,
    payload: { revision: 5 },
  });
  ws.message({
    protocol_version: 1,
    type: "state.snapshot",
    payload: {
      revision: 5,
      selected_media: true,
      selected_media_name: "one.mp3",
      active_media_name: "one.mp3",
      artwork_id: "cover",
      has_session: true,
      playback_state: "PLAYING",
      queue: [active],
    },
  });
  assert.equal(ids.queue.children.length, 1);
  assert.equal(
    ids.queue.children[0].children[1].children[0].textContent,
    "one.mp3",
  );
  assert.equal(ids.artwork.src, "/api/artwork/cover.jpg");
  assert.equal(ids["artwork-placeholder"].hidden, true);
  assert.equal(ids["play-toggle"].dataset.icon, "pause");
  assert.equal(ids["play-toggle"].disabled, false);
  assert.equal(ids.queue.children[0].children[2].children[0].disabled, false);
});

test("theme toggle cycles auto, light, dark and follows the system only in auto", async () => {
  const { ids, document, env, mql } = fixture();
  mql.matches = true;
  startClient(env);
  await settle();
  const toggle = ids["theme-toggle"],
    theme = () => document.documentElement.dataset.theme;
  assert.equal(theme(), "dark");
  assert.equal(toggle.dataset.mode, "auto");
  assert.equal(toggle.title, "Theme: Auto");
  toggle.emit("click");
  assert.equal(theme(), "light");
  assert.equal(env.localStorage.getItem("go2tv-theme"), "light");
  assert.equal(toggle.title, "Theme: Light");
  mql.emit();
  assert.equal(theme(), "light");
  toggle.emit("click");
  assert.equal(theme(), "dark");
  assert.equal(env.localStorage.getItem("go2tv-theme"), "dark");
  assert.equal(toggle.dataset.mode, "dark");
  toggle.emit("click");
  assert.equal(env.localStorage.getItem("go2tv-theme"), "auto");
  assert.equal(theme(), "dark");
  mql.matches = false;
  mql.emit();
  assert.equal(theme(), "light");
  const second = fixture();
  second.env.localStorage.setItem("go2tv-theme", "dark");
  startClient(second.env);
  await settle();
  assert.equal(second.document.documentElement.dataset.theme, "dark");
  assert.equal(second.ids["theme-toggle"].title, "Theme: Dark");
});

test("back to top appears after scrolling and returns smoothly", async () => {
  const { ids, window, env } = fixture();
  startClient(env);
  await settle();
  assert.equal(ids["back-to-top"].dataset.visible, "false");
  assert.equal(ids["back-to-top"].ariaHidden, "true");
  assert.equal(ids["back-to-top"].tabIndex, -1);
  window.scrollY = 500;
  window.emit("scroll");
  assert.equal(ids["back-to-top"].dataset.visible, "true");
  assert.equal(ids["back-to-top"].ariaHidden, "false");
  assert.equal(ids["back-to-top"].tabIndex, 0);
  ids["back-to-top"].emit("click");
  assert.deepEqual(window.scrolledTo, { top: 0, behavior: "smooth" });
  window.scrollY = 10;
  window.emit("scroll");
  assert.equal(ids["back-to-top"].dataset.visible, "false");
  assert.equal(ids["back-to-top"].ariaHidden, "true");
  assert.equal(ids["back-to-top"].tabIndex, -1);
});

test("loop and autoplay remain mutually exclusive", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  ids.autoplay.checked = true;
  ids["same-type"].checked = true;
  ids.gapless.checked = true;
  ids.loop.checked = true;
  ids.loop.emit("change");
  assert.equal(ids.autoplay.checked, false);
  assert.equal(ids["same-type"].checked, false);
  assert.equal(ids.gapless.checked, false);
  assert.deepEqual(ws.sent.at(-1).payload.policy, {
    LoopSelected: true,
    AutoPlayNext: false,
    AutoPlaySameType: false,
    GaplessEnabled: false,
    ImageDurationSeconds: 10,
  });
  ids.autoplay.checked = true;
  ids.autoplay.emit("change");
  assert.equal(ids.loop.checked, false);
  assert.equal(ws.sent.at(-1).payload.policy.AutoPlayNext, true);
});

test("normalizes image duration to desktop limits", async () => {
  const { ids, env } = fixture();
  startClient(env);
  await settle();
  const ws = FakeSocket.instances[0];
  for (const [input, want] of [
    ["0", 0],
    ["1", 5],
    ["4.9", 5],
    ["300", 300],
    ["301", 300],
    ["-1", 0],
    ["", 0],
  ]) {
    ids["image-duration"].value = input;
    ids["image-duration"].emit("change");
    assert.equal(ws.sent.at(-1).payload.policy.ImageDurationSeconds, want);
    assert.equal(ids["image-duration"].value, String(want));
  }
});
