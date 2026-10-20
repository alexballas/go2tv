const modes = ["discover", "favorites", "recent"];
const searchDelay = 400;
const directoryErrors = {
  radio_timeout: "The radio directory timed out. Try again.",
  radio_unavailable:
    "The radio directory is unavailable right now. Try again later.",
  radio_unreachable:
    "Can't reach the radio directory. Check the server's internet connection.",
};
const placeholders = {
  discover: "Search stations or genres",
  favorites: "Filter favorites",
  recent: "Filter recent stations",
};

export function radioControls(env, ui) {
  const { document, fetch, localStorage, setTimeout, clearTimeout } = env;
  const byID = (id) => document.querySelector(`#${id}`);
  const tabs = { library: byID("source-library"), radio: byID("source-radio") },
    panels = { library: byID("library-panel"), radio: byID("radio-panel") },
    tools = { library: byID("library-tools"), radio: byID("radio-tools") },
    modeButtons = Object.fromEntries(
      modes.map((name) => [name, byID(`radio-${name}`)]),
    ),
    query = byID("radio-query"),
    country = byID("radio-country"),
    list = byID("radio-list"),
    status = byID("radio-status"),
    favoritesCount = byID("radio-favorites-count"),
    liveDot = byID("radio-live-dot");
  const read = (key) => {
    try {
      return localStorage.getItem(key) || "";
    } catch {
      return "";
    }
  };
  const write = (key, value) => {
    try {
      localStorage.setItem(key, value);
    } catch {}
  };
  let enabled = false,
    source = read("go2tv-source") === "radio" ? "radio" : "library",
    mode = modes.includes(read("go2tv-radio-mode"))
      ? read("go2tv-radio-mode")
      : "",
    countryCode = read("go2tv-radio-country"),
    favorites = [],
    recent = [],
    countries = [],
    countriesState = "idle",
    discover = { state: "idle", stations: [], error: "", key: "" },
    generation = 0,
    searchTimer,
    listKey = "";
  const rows = new Map();

  const station = (id) =>
    favorites.find((item) => item.id === id) ||
    recent.find((item) => item.id === id) ||
    discover.stations.find((item) => item.id === id);
  const isFavorite = (id) => favorites.some((item) => item.id === id);
  const searchKey = () => JSON.stringify([query.value.trim(), countryCode]);
  const pendingFor = (type, id) =>
    ui
      .pendingRequests()
      .some(
        (request) =>
          request?.type === type && request.payload?.station_id === id,
      );
  const countryName = (code) =>
    countries.find((item) => item.code === code)?.name || code;
  const stationMeta = (item) => {
    const quality = [item.codec, item.bitrate ? `${item.bitrate} kbps` : ""]
      .filter(Boolean)
      .join(" ");
    return [item.country || item.country_code, quality, item.tags?.join(", ")]
      .filter(Boolean)
      .join(" · ");
  };
  const matches = (item) => {
    const text = query.value.trim().toLowerCase();
    return (
      (!countryCode || item.country_code === countryCode) &&
      (!text ||
        [item.name, item.country, ...(item.tags || [])].some((value) =>
          String(value || "")
            .toLowerCase()
            .includes(text),
        ))
    );
  };

  function setSource(next, focus = false) {
    source = enabled && next === "radio" ? "radio" : "library";
    write("go2tv-source", source);
    if (source === "radio") open();
    render();
    if (focus) tabs[source].focus();
  }

  function setMode(next) {
    mode = next;
    write("go2tv-radio-mode", mode);
    if (mode === "discover" && discover.key !== searchKey()) search();
    render();
  }

  // The first visit lands on saved favorites when there are any, since those
  // are the stations a remote is most often reopened for.
  function open() {
    if (!mode) mode = favorites.length ? "favorites" : "discover";
    if (countriesState === "idle") loadCountries();
    if (mode === "discover" && discover.key !== searchKey()) search();
  }

  async function getJSON(url) {
    const response = await fetch(url, {
        headers: { Accept: "application/json" },
      }),
      body = await response.json();
    if (!response.ok) throw new Error(body.error || "radio_unreachable");
    return body;
  }

  async function loadCountries() {
    countriesState = "loading";
    try {
      countries = (await getJSON("/api/radio/countries")).countries || [];
      countriesState = "ready";
    } catch {
      // Retried the next time the country list is opened.
      countriesState = "idle";
    }
    renderCountries();
    render();
  }

  function renderCountries() {
    const selected = countryCode,
      all = document.createElement("option");
    all.value = "";
    all.textContent = "All countries";
    country.replaceChildren(all);
    let found = !selected;
    for (const item of countries) {
      const node = document.createElement("option");
      node.value = item.code;
      node.textContent = item.name;
      country.append(node);
      found ||= item.code === selected;
    }
    // Keep a remembered country selectable before the list arrives.
    if (!found) {
      const node = document.createElement("option");
      node.value = selected;
      node.textContent = selected;
      country.append(node);
    }
    country.value = selected;
  }

  async function search() {
    clearTimeout(searchTimer);
    const current = ++generation,
      key = searchKey(),
      params = new URLSearchParams();
    if (query.value.trim()) params.set("q", query.value.trim());
    if (countryCode) params.set("country", countryCode);
    discover = { ...discover, state: "loading", key };
    render();
    try {
      const suffix = String(params),
        body = await getJSON(
          `/api/radio/stations${suffix ? `?${suffix}` : ""}`,
        );
      if (current !== generation) return;
      discover = {
        state: "ready",
        stations: body.stations || [],
        error: "",
        key,
      };
    } catch (error) {
      if (current !== generation) return;
      discover = {
        state: "error",
        stations: [],
        error:
          directoryErrors[error.message] || directoryErrors.radio_unreachable,
        key,
      };
    }
    render();
  }

  function scheduleSearch() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(search, searchDelay);
  }

  function visible() {
    if (mode === "favorites")
      return [...favorites]
        .sort((a, b) =>
          a.name.localeCompare(b.name, undefined, { sensitivity: "base" }),
        )
        .filter(matches);
    if (mode === "recent") return recent.filter(matches);
    return discover.stations;
  }

  function statusText(stations) {
    const text = query.value.trim(),
      place = countryCode ? ` in ${countryName(countryCode)}` : "";
    if (mode === "favorites")
      return favorites.length
        ? `${stations.length} of ${favorites.length} favorites`
        : "";
    if (mode === "recent") return recent.length ? "Most recent first" : "";
    if (discover.state === "loading") return "Finding stations…";
    if (discover.state !== "ready" || !stations.length) return "";
    return text
      ? `${stations.length} stations for “${text}”${place}`
      : `Popular stations${place || " worldwide"}`;
  }

  function emptyState() {
    const node = document.createElement("li");
    node.className = "empty-state";
    if (mode === "discover" && discover.state === "error") {
      const message = document.createElement("p"),
        retry = document.createElement("button");
      message.textContent = discover.error;
      retry.type = "button";
      retry.textContent = "Try again";
      retry.addEventListener("click", search);
      node.append(message, retry);
      return node;
    }
    const filtered = query.value.trim() || countryCode;
    node.textContent =
      mode === "favorites" && !favorites.length
        ? "No favorites yet. Tap the star on any station to keep it here."
        : mode === "recent" && !recent.length
          ? "Stations you play will appear here."
          : mode === "discover"
            ? "No stations found. Try a genre, or another country."
            : filtered
              ? "No matches."
              : "";
    return node;
  }

  function skeleton() {
    return Array.from({ length: 6 }, () => {
      const row = document.createElement("li"),
        logo = document.createElement("span"),
        lines = document.createElement("span");
      row.className = "library-row radio-row skeleton-row";
      row.ariaHidden = "true";
      logo.className = "station-logo";
      lines.className = "skeleton-lines";
      lines.append(document.createElement("i"), document.createElement("i"));
      row.append(logo, lines);
      return row;
    });
  }

  function stationRow(item) {
    const row = document.createElement("li"),
      main = document.createElement("div"),
      logo = document.createElement("span"),
      glyph = ui.icon("radio"),
      equalizer = document.createElement("span"),
      copy = document.createElement("div"),
      name = document.createElement("strong"),
      meta = document.createElement("span"),
      favorite = document.createElement("button"),
      play = document.createElement("button"),
      actions = document.createElement("div");
    row.className = "library-row radio-row";
    main.className = "entry-main";
    logo.className = "station-logo";
    logo.ariaHidden = "true";
    equalizer.className = "equalizer";
    equalizer.append(
      document.createElement("i"),
      document.createElement("i"),
      document.createElement("i"),
    );
    logo.append(glyph);
    if (item.icon_url) {
      const image = document.createElement("img");
      image.alt = "";
      image.loading = "lazy";
      image.decoding = "async";
      // Lazy images load only once rendered, so fade in rather than hide.
      image.addEventListener("load", () => {
        image.dataset.loaded = "true";
        glyph.hidden = true;
      });
      image.addEventListener("error", () => image.remove());
      image.src = item.icon_url;
      logo.append(image);
    }
    logo.append(equalizer);
    copy.className = "entry-copy";
    name.className = "entry-name";
    name.textContent = item.name;
    name.title = item.name;
    meta.className = "entry-meta";
    meta.textContent = stationMeta(item) || "Internet radio";
    meta.title = meta.textContent;
    copy.append(name, meta);
    main.append(logo, copy);
    favorite.type = "button";
    favorite.className = "icon-action favorite-action";
    favorite.addEventListener("click", () => toggleFavorite(item.id));
    play.type = "button";
    play.className = "primary-action icon-action";
    play.addEventListener("click", () => primaryAction(item.id).run());
    actions.className = "row-actions";
    actions.append(favorite, play);
    row.append(main, actions);
    rows.set(item.id, { row, favorite, play, name: item.name });
    return row;
  }

  function primaryAction(id) {
    const state = ui.state(),
      current = ui.playbackState(),
      active = state.has_session && state.active_station === id,
      starting =
        pendingFor("radio.play", id) ||
        (current === "LOADING" && state.selected_station === id);
    if (starting)
      return {
        label: "Tuning in…",
        icon: "loader-circle",
        spin: true,
        disabled: true,
        run() {},
      };
    if (active && current === "PLAYING")
      return {
        label: "Pause",
        icon: "pause",
        run: () => ui.send("player.pause"),
      };
    if (active && current === "PAUSED")
      return {
        label: "Resume",
        icon: "play",
        run: () => ui.send("player.resume"),
      };
    return { label: "Play", icon: "play", run: () => play(id) };
  }

  function play(id) {
    if (!ui.canMutate()) return;
    ui.send("radio.play", { station_id: id });
  }

  function toggleFavorite(id) {
    if (!ui.canMutate() || pendingFor("radio.favorite", id)) return;
    ui.send("radio.favorite", { station_id: id, favorite: !isFavorite(id) });
  }

  // Rows persist across playback updates so logos never reload; only their
  // state attributes and buttons follow the snapshot.
  function syncRows() {
    const state = ui.state(),
      current = ui.playbackState(),
      locked = !ui.canMutate();
    for (const [id, parts] of rows) {
      const active = state.has_session && state.active_station === id,
        selected =
          !active && !state.has_session && state.selected_station === id,
        favorite = isFavorite(id),
        action = primaryAction(id),
        favoriteLabel = favorite
          ? `Remove ${parts.name} from favorites`
          : `Add ${parts.name} to favorites`;
      parts.row.dataset.playing = String(active);
      parts.row.dataset.paused = String(active && current === "PAUSED");
      parts.row.dataset.selected = String(selected);
      if (active || selected) parts.row.ariaCurrent = "true";
      else parts.row.removeAttribute("aria-current");
      ui.setButtonIcon(
        parts.play,
        action.icon,
        `${action.label.replace("…", "")} ${parts.name}`,
        action.spin,
      );
      parts.play.title = action.label;
      parts.play.disabled = locked || !!action.disabled;
      ui.setButtonIcon(parts.favorite, "star", favoriteLabel);
      parts.favorite.title = favorite
        ? "Remove from favorites"
        : "Add to favorites";
      parts.favorite.ariaPressed = String(favorite);
      parts.favorite.disabled = locked || pendingFor("radio.favorite", id);
    }
  }

  function renderList() {
    const stations = visible(),
      loading = mode === "discover" && discover.state === "loading",
      key = JSON.stringify([
        mode,
        loading,
        discover.state,
        stations.map((item) => [item.id, item.name, item.icon_url]),
      ]);
    status.textContent = statusText(stations);
    if (key === listKey) return;
    listKey = key;
    rows.clear();
    if (loading) list.replaceChildren(...skeleton());
    else if (!stations.length) list.replaceChildren(emptyState());
    else list.replaceChildren(...stations.map(stationRow));
  }

  function render() {
    tabs.radio.hidden = !enabled;
    if (!enabled) source = "library";
    for (const name of ["library", "radio"]) {
      const selected = source === name;
      tabs[name].ariaSelected = String(selected);
      tabs[name].tabIndex = selected ? 0 : -1;
      panels[name].hidden = !selected;
      tools[name].hidden = !selected;
    }
    const state = ui.state();
    liveDot.hidden = !(state.has_session && state.active_live);
    favoritesCount.textContent = favorites.length
      ? String(favorites.length)
      : "";
    favoritesCount.hidden = !favorites.length;
    for (const name of modes)
      modeButtons[name].ariaPressed = String(mode === name);
    query.placeholder = placeholders[mode] || placeholders.discover;
    query.ariaLabel = query.placeholder;
    if (source !== "radio") return;
    renderList();
    syncRows();
  }

  for (const name of ["library", "radio"]) {
    tabs[name].addEventListener("click", () => setSource(name));
    tabs[name].addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key))
        return;
      event.preventDefault();
      setSource(
        event.key === "Home" || (event.key === "ArrowLeft" && name === "radio")
          ? "library"
          : event.key === "End" || name === "library"
            ? "radio"
            : "library",
        true,
      );
    });
  }
  for (const name of modes)
    modeButtons[name].addEventListener("click", () => setMode(name));
  query.addEventListener("input", () => {
    if (mode === "discover") scheduleSearch();
    else render();
  });
  tools.radio.addEventListener("submit", (event) => {
    event.preventDefault();
    if (mode === "discover") search();
  });
  country.addEventListener("change", () => {
    countryCode = country.value;
    write("go2tv-radio-country", countryCode);
    if (mode === "discover") search();
    else render();
  });
  country.addEventListener("focus", () => {
    if (countriesState === "idle") loadCountries();
  });

  return {
    render,
    isFavorite,
    toggleFavorite,
    station,
    configure(features, library) {
      enabled = !!features?.radio;
      favorites = library?.favorites || [];
      recent = library?.recent || [];
      renderCountries();
      if (enabled && source === "radio") open();
      render();
    },
    setLibrary(library) {
      favorites = library?.favorites || [];
      recent = library?.recent || [];
      render();
    },
  };
}
