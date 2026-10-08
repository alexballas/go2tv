export function torrentControls(env, send, canMutate, isPending, showToast) {
  const { document, fetch, setTimeout, clearTimeout } = env;
  const node = (id) => document.querySelector(`#torrent-${id}`);
  let enabled = false,
    data = {},
    loading = false,
    timer,
    polling = false,
    choiceKey = "",
    maxBytes = 4 << 20,
    generation = 0;

  function render() {
    node("panel").hidden = !enabled;
    const pending = data.pending,
      active = data.active,
      disabled = !canMutate() || loading || isPending();
    node("magnet").disabled = disabled;
    node("load").disabled = disabled;
    node("upload").disabled = disabled;
    node("status").textContent = loading
      ? "Loading torrent…"
      : pending?.status === "loading"
        ? "Fetching torrent metadata…"
        : pending?.status === "error"
          ? pending.error || "Could not load torrent."
          : pending?.status === "ready"
            ? "Choose a file, then press Play to cast. Missing pieces buffer automatically."
            : "";
    const ready = pending?.status === "ready",
      key = `${pending?.id || ""}:${ready}`;
    node("choices").hidden = !ready;
    if (key !== choiceKey) {
      choiceKey = key;
      node("files").replaceChildren();
      for (const file of ready ? pending.files || [] : []) {
        const option = document.createElement("option");
        option.value = String(file.index);
        option.textContent = `${file.name} (${(file.size / (1 << 20)).toFixed(1)} MiB)`;
        node("files").append(option);
      }
    }
    node("files").disabled = disabled || !ready;
    node("select").disabled = disabled || !ready;
    node("cancel-pending").hidden = !pending;
    node("cancel-pending").disabled = disabled;
    node("download").hidden = !active;
    node("cancel").disabled = disabled;
    if (active) {
      const progress = active.total > 0 ? active.completed / active.total : 0;
      node("progress").value = progress;
      node("progress-text").textContent =
        `${active.name} · ${active.completed > 0 ? `${(100 * progress).toFixed(1)}% · ${(active.completed / (1 << 20)).toFixed(1)} / ${(active.total / (1 << 20)).toFixed(1)} MiB` : "Waiting for peers…"}`;
    }
  }

  async function refresh() {
    if (!enabled || !polling) return;
    const current = ++generation;
    try {
      const response = await fetch("/api/torrent", {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error("Torrent status unavailable");
      const status = await response.json();
      if (generation === current) {
        data = status;
        render();
      }
    } catch {
      // Connection status/reconnect already communicates server failures.
    } finally {
      clearTimeout(timer);
      if (enabled && polling) timer = setTimeout(refresh, 1000);
    }
  }

  async function load(body, contentType) {
    if (!enabled || !canMutate() || loading || isPending()) return;
    loading = true;
    generation++;
    render();
    try {
      const response = await fetch("/api/torrent", {
        method: "POST",
        headers: { "Content-Type": contentType, Accept: "application/json" },
        body,
      });
      const result = await response.json();
      if (!response.ok)
        throw new Error(result.error || "Could not load torrent");
      data = result;
      node("input").open = true;
    } catch (error) {
      showToast(error.message, "error");
    } finally {
      loading = false;
      render();
      refresh();
    }
  }

  node("form").addEventListener("submit", (event) => {
    event.preventDefault();
    const magnet = node("magnet").value.trim();
    if (!/^magnet:/i.test(magnet)) {
      showToast("Enter a magnet link.", "error");
      return;
    }
    load(JSON.stringify({ magnet }), "application/json");
  });
  node("upload").addEventListener("change", () => {
    const file = node("upload").files?.[0];
    node("upload").value = "";
    if (!file) return;
    if (!/\.torrent$/i.test(file.name)) {
      showToast("Choose a .torrent file.", "error");
      return;
    }
    if (file.size > maxBytes) {
      showToast(
        `Torrent file exceeds ${(maxBytes / (1 << 20)).toFixed(0)} MiB.`,
        "error",
      );
      return;
    }
    load(file, "application/x-bittorrent");
  });
  node("select").addEventListener("click", () => {
    if (!canMutate() || isPending() || data.pending?.status !== "ready") return;
    send("torrent.select", {
      torrent_id: data.pending.id,
      index: Number(node("files").value),
    });
  });
  for (const [button, key] of [
    ["cancel-pending", "pending"],
    ["cancel", "active"],
  ])
    node(button).addEventListener("click", () => {
      if (!canMutate() || isPending() || !data[key]) return;
      send("torrent.cancel", { torrent_id: data[key].id });
    });

  return {
    render,
    refresh,
    configure(features, initial, limit) {
      enabled = !!features?.torrent;
      data = initial || {};
      maxBytes = limit || maxBytes;
      render();
    },
    connection(connected) {
      polling = connected;
      clearTimeout(timer);
      if (polling) refresh();
      render();
    },
  };
}
