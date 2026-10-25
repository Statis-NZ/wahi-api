(() => {
  // --- State & Elements ---
  const STORAGE_KEY = 'wahi_demo_settings_v1';
  const baseUrlInput = document.getElementById('api-base-url');
  const tokenInput = document.getElementById('api-token');
  const authModeSelect = document.getElementById('auth-mode');
  const openapiLink = document.getElementById('openapi-link');
  const healthBadge = document.getElementById('health-badge');
  const healthText = document.getElementById('health-text');
  const indexCounts = document.getElementById('index-counts');

  const searchInput = document.getElementById('autosuggest-q');
  const suggestionsList = document.getElementById('suggestions-list');
  const autosuggestTiming = document.getElementById('autosuggest-timing');
  const filterType = document.getElementById('filter-type');
  const filterTown = document.getElementById('filter-town');
  const filterLimit = document.getElementById('filter-limit');
  const filterLat = document.getElementById('filter-lat');
  const filterLon = document.getElementById('filter-lon');
  const filterProposed = document.getElementById('filter-proposed');

  const lookupIdInput = document.getElementById('lookup-address-id');
  const formStreet = document.getElementById('form-street-address');
  const formSuburb = document.getElementById('form-suburb');
  const formCity = document.getElementById('form-city');
  const formTa = document.getElementById('form-ta');
  const formCoords = document.getElementById('form-coords');
  const decomposedContainer = document.getElementById('decomposed-fields');

  const revLatInput = document.getElementById('rev-lat');
  const revLonInput = document.getElementById('rev-lon');
  const revRadiusInput = document.getElementById('rev-radius');
  const revLimitInput = document.getElementById('rev-limit');

  const inspectorUrl = document.getElementById('inspector-url');
  const inspectorStatus = document.getElementById('inspector-status');
  const inspectorJson = document.getElementById('inspector-json');
  const copyCurlBtn = document.getElementById('copy-curl-btn');
  const mapCoordsReadout = document.getElementById('map-coords-readout');

  let currentSuggestions = [];
  let activeIndex = -1;
  let debounceTimer = null;
  let lastCurlCommand = '';

  // Restore saved settings
  try {
    const saved = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}');
    if (saved.baseUrl) baseUrlInput.value = saved.baseUrl;
    if (saved.authMode) authModeSelect.value = saved.authMode;
  } catch (_) {}

  function getBaseUrl() {
    return baseUrlInput.value.trim().replace(/\/+$/, '') || 'http://localhost:8080';
  }

  function saveSettings() {
    const base = getBaseUrl();
    openapiLink.href = `${base}/docs`;
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        baseUrl: base,
        authMode: authModeSelect.value,
      }));
    } catch (_) {}
  }

  // --- API Fetch Helper with Inspector Logging ---
  async function apiRequest(path, queryParams = {}, { logToInspector = true } = {}) {
    saveSettings();
    const url = new URL(`${getBaseUrl()}${path}`);
    Object.entries(queryParams).forEach(([k, v]) => {
      if (v !== undefined && v !== null && v !== '') {
        url.searchParams.set(k, String(v));
      }
    });

    const headers = { Accept: 'application/json' };
    const token = tokenInput.value.trim();
    const mode = authModeSelect.value;

    if (token) {
      if (mode === 'bearer') {
        headers['Authorization'] = `Bearer ${token}`;
      } else if (mode === 'header') {
        headers['X-API-Key'] = token;
      } else if (mode === 'query') {
        url.searchParams.set('api_key', token);
      }
    }

    // Build equivalent cURL command
    const headerFlags = Object.entries(headers)
      .filter(([k]) => k !== 'Accept')
      .map(([k, v]) => `-H ${JSON.stringify(`${k}: ${v}`)}`)
      .join(' ');
    lastCurlCommand = `curl ${headerFlags ? headerFlags + ' ' : ''}${JSON.stringify(url.toString())}`;

    const t0 = performance.now();
    let response;
    let payload;
    try {
      response = await fetch(url.toString(), { headers });
      const text = await response.text();
      try {
        payload = JSON.parse(text);
      } catch {
        payload = text;
      }
    } catch (networkErr) {
      if (logToInspector) {
        inspectorUrl.textContent = `GET ${url.toString()}`;
        inspectorStatus.className = 'badge err';
        inspectorStatus.textContent = 'Network / CORS Error';
        inspectorJson.textContent = JSON.stringify({
          error: networkErr.message,
          hint: 'Ensure Wāhi API is running at ' + getBaseUrl() + ' and CORS_ORIGINS allows this origin.',
        }, null, 2);
      }
      throw networkErr;
    }
    const elapsedMs = Math.round(performance.now() - t0);

    if (logToInspector) {
      inspectorUrl.textContent = `GET ${url.toString()}`;
      inspectorStatus.className = response.ok ? 'badge ok' : 'badge err';
      inspectorStatus.textContent = `HTTP ${response.status} (${elapsedMs} ms)`;
      inspectorJson.textContent = typeof payload === 'string'
        ? payload
        : JSON.stringify(payload, null, 2);
    }

    return { ok: response.ok, status: response.status, data: payload, elapsedMs };
  }

  // --- Leaflet Map Setup ---
  let map = null;
  let markerLayer = null;
  let radiusCircle = null;

  if (window.L) {
    map = L.map('map').setView([-36.8485, 174.7633], 14);
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors | LINZ CC-BY 4.0',
    }).addTo(map);
    markerLayer = L.layerGroup().addTo(map);

    map.on('click', (e) => {
      const lat = Number(e.latlng.lat.toFixed(6));
      const lon = Number(e.latlng.lng.toFixed(6));
      revLatInput.value = lat;
      revLonInput.value = lon;
      mapCoordsReadout.textContent = `${lat}, ${lon}`;
      runReverseGeocode();
    });
  } else {
    document.getElementById('map').innerHTML =
      '<div style="padding:1rem;color:var(--text-muted);">Map tiles unavailable offline; API testing remains fully functional.</div>';
  }

  function plotMapLocation(lat, lon, label, radiusM = 0) {
    if (!map || !markerLayer || typeof lat !== 'number' || typeof lon !== 'number') return;
    markerLayer.clearLayers();
    if (radiusCircle) {
      map.removeLayer(radiusCircle);
      radiusCircle = null;
    }
    const marker = L.marker([lat, lon]).addTo(markerLayer);
    if (label) {
      marker.bindPopup(label).openPopup();
    }
    if (radiusM > 0) {
      radiusCircle = L.circle([lat, lon], {
        radius: radiusM,
        color: '#0284c7',
        fillColor: '#38bdf8',
        fillOpacity: 0.15,
        weight: 2,
      }).addTo(map);
    }
    map.setView([lat, lon], Math.max(map.getZoom(), 16));
    mapCoordsReadout.textContent = `${lat.toFixed(5)}, ${lon.toFixed(5)}`;
  }

  // --- 0. Health Check (/healthz) ---
  async function checkHealth(logToInspector = false) {
    try {
      const { ok, data } = await apiRequest('/healthz', {}, { logToInspector });
      if (ok && data && data.status === 'ok') {
        healthBadge.className = 'badge ok';
        healthText.textContent = `API OK (Meilisearch: ${data.meilisearch_status})`;
        const idx = data.indexes || {};
        indexCounts.textContent =
          `Addresses: ${(idx.addresses ?? 0).toLocaleString()} | ` +
          `Streets: ${(idx.streets ?? 0).toLocaleString()} | ` +
          `Localities: ${(idx.localities ?? 0).toLocaleString()}`;
      } else {
        healthBadge.className = 'badge err';
        healthText.textContent = 'API Degraded';
      }
    } catch (_) {
      healthBadge.className = 'badge err';
      healthText.textContent = 'API Unreachable';
      indexCounts.textContent = 'Addresses: – | Streets: – | Localities: –';
    }
  }

  // --- 1. Autosuggest (/v1/autosuggest) ---
  async function runAutosuggest() {
    const q = searchInput.value.trim();
    if (q.length < 2) {
      closeSuggestions();
      return;
    }

    const params = {
      q,
      type: filterType.value,
      limit: filterLimit.value,
      town_city: filterTown.value.trim(),
      lat: filterLat.value.trim(),
      lon: filterLon.value.trim(),
      include_proposed: filterProposed.checked ? 'true' : undefined,
    };

    try {
      const { ok, data, elapsedMs } = await apiRequest('/v1/autosuggest', params);
      if (!ok || !data || !Array.isArray(data.results)) {
        closeSuggestions();
        return;
      }
      autosuggestTiming.textContent = `${data.query_time_ms ?? 0} ms (${elapsedMs} ms RTT)`;
      renderSuggestions(data.results);
    } catch (_) {
      closeSuggestions();
    }
  }

  function renderSuggestions(items) {
    currentSuggestions = items;
    activeIndex = -1;
    suggestionsList.innerHTML = '';

    if (items.length === 0) {
      const li = document.createElement('li');
      li.className = 'suggestion-item';
      li.style.cursor = 'default';
      li.innerHTML = `<div class="suggestion-main"><span class="suggestion-sub">No matching NZ addresses, streets, or localities found.</span></div>`;
      suggestionsList.appendChild(li);
      suggestionsList.hidden = false;
      searchInput.setAttribute('aria-expanded', 'true');
      return;
    }

    items.forEach((item, index) => {
      const li = document.createElement('li');
      li.className = 'suggestion-item';
      li.id = `suggestion-${index}`;
      li.setAttribute('role', 'option');
      li.setAttribute('aria-selected', 'false');

      const main = document.createElement('div');
      main.className = 'suggestion-main';

      const title = document.createElement('span');
      title.className = 'suggestion-title';
      title.textContent = item.full_address;

      const sub = document.createElement('span');
      sub.className = 'suggestion-sub';
      const metaParts = [`ID: ${item.id}`];
      if (item.suburb_locality) metaParts.push(item.suburb_locality);
      if (item.town_city && item.town_city !== item.suburb_locality) metaParts.push(item.town_city);
      if (item.location) {
        metaParts.push(`(${item.location.lat.toFixed(4)}, ${item.location.lon.toFixed(4)})`);
      }
      sub.textContent = metaParts.join(' · ');

      main.appendChild(title);
      main.appendChild(sub);

      const tag = document.createElement('span');
      tag.className = `type-tag ${item.type || ''}`;
      tag.textContent = item.type || 'item';

      li.appendChild(main);
      li.appendChild(tag);

      li.addEventListener('click', () => selectSuggestion(item));
      suggestionsList.appendChild(li);
    });

    suggestionsList.hidden = false;
    searchInput.setAttribute('aria-expanded', 'true');
  }

  function closeSuggestions() {
    suggestionsList.hidden = true;
    searchInput.setAttribute('aria-expanded', 'false');
    activeIndex = -1;
  }

  async function selectSuggestion(item) {
    searchInput.value = item.full_address;
    closeSuggestions();

    // Populate form fields immediately from suggestion
    const streetLine = [item.full_address_number, item.full_road_name].filter(Boolean).join(' ') || item.full_address;
    formStreet.value = streetLine;
    formSuburb.value = item.suburb_locality || '';
    formCity.value = item.town_city || '';
    formTa.value = '';
    if (item.location) {
      formCoords.value = `${item.location.lat}, ${item.location.lon}`;
      plotMapLocation(item.location.lat, item.location.lon, item.full_address);
    } else {
      formCoords.value = '';
    }

    // If this is an address record, also fetch the full decomposed LINZ document
    if (item.type === 'address' && item.id) {
      lookupIdInput.value = item.id;
      await fetchAddressById(item.id);
    } else {
      decomposedContainer.hidden = true;
    }
  }

  // --- 2. Address Lookup by ID (/v1/addresses/{id}) ---
  async function fetchAddressById(addressId) {
    if (!addressId) return;
    try {
      const { ok, data } = await apiRequest(`/v1/addresses/${encodeURIComponent(addressId)}`);
      if (!ok || !data || !data.address_id) {
        decomposedContainer.hidden = true;
        return;
      }

      const streetLine = [data.full_address_number, data.full_road_name].filter(Boolean).join(' ') || data.full_address;
      formStreet.value = streetLine;
      formSuburb.value = data.suburb_locality || '';
      formCity.value = data.town_city || '';
      formTa.value = data.territorial_authority || '';

      if (data.location) {
        formCoords.value = `${data.location.lat}, ${data.location.lon}`;
        plotMapLocation(data.location.lat, data.location.lon, data.full_address);
      }

      renderDecomposedFields(data);
    } catch (_) {
      decomposedContainer.hidden = true;
    }
  }

  function renderDecomposedFields(doc) {
    const fields = [
      ['address_id', doc.address_id],
      ['road_id', doc.road_id],
      ['full_address_number', doc.full_address_number],
      ['unit', doc.unit || '—'],
      ['address_number', doc.address_number],
      ['address_number_suffix', doc.address_number_suffix || '—'],
      ['address_number_high', doc.address_number_high ?? '—'],
      ['road_name', doc.road_name || '—'],
      ['road_name_type', doc.road_name_type || '—'],
      ['road_name_suffix', doc.road_name_suffix || '—'],
      ['road_name_label', doc.road_name_label || '—'],
      ['secondary_road_name', doc.secondary_road_name || '—'],
      ['address_lifecycle', doc.address_lifecycle || '—'],
      ['is_land', String(doc.is_land)],
    ];

    decomposedContainer.innerHTML = '';
    fields.forEach(([key, val]) => {
      const div = document.createElement('div');
      div.className = 'kv-item';
      const kSpan = document.createElement('span');
      kSpan.className = 'kv-key';
      kSpan.textContent = key;
      const vSpan = document.createElement('span');
      vSpan.className = 'kv-val';
      vSpan.textContent = String(val);
      div.appendChild(kSpan);
      div.appendChild(vSpan);
      decomposedContainer.appendChild(div);
    });
    decomposedContainer.hidden = false;
  }

  // --- 3. Reverse Geocode (/v1/reverse) ---
  async function runReverseGeocode() {
    const lat = parseFloat(revLatInput.value);
    const lon = parseFloat(revLonInput.value);
    const radiusM = parseInt(revRadiusInput.value, 10) || 100;
    const limit = parseInt(revLimitInput.value, 10) || 5;

    if (Number.isNaN(lat) || Number.isNaN(lon)) return;

    try {
      const { ok, data } = await apiRequest('/v1/reverse', {
        lat,
        lon,
        radius_m: radiusM,
        limit,
      });

      if (!ok || !data || !Array.isArray(data.results)) return;

      const top = data.results[0];
      const popupText = top
        ? `<strong>Nearest (${data.results.length} found):</strong><br>${top.full_address}`
        : `No addresses within ${radiusM}m`;
      plotMapLocation(lat, lon, popupText, radiusM);

      if (map && markerLayer && data.results.length > 1) {
        data.results.slice(1).forEach((item) => {
          if (item.location) {
            L.circleMarker([item.location.lat, item.location.lon], {
              radius: 6,
              color: '#0369a1',
              fillColor: '#38bdf8',
              fillOpacity: 0.85,
            })
              .bindPopup(item.full_address)
              .addTo(markerLayer);
          }
        });
      }
    } catch (_) {}
  }

  // --- Event Listeners ---
  document.getElementById('connection-form').addEventListener('submit', (e) => {
    e.preventDefault();
    checkHealth(true);
  });

  searchInput.addEventListener('input', () => {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(runAutosuggest, 140);
  });

  searchInput.addEventListener('keydown', (e) => {
    if (suggestionsList.hidden || currentSuggestions.length === 0) return;
    const options = suggestionsList.querySelectorAll('.suggestion-item');

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      activeIndex = (activeIndex + 1) % currentSuggestions.length;
      updateActiveOption(options);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      activeIndex = (activeIndex - 1 + currentSuggestions.length) % currentSuggestions.length;
      updateActiveOption(options);
    } else if (e.key === 'Enter' && activeIndex >= 0) {
      e.preventDefault();
      selectSuggestion(currentSuggestions[activeIndex]);
    } else if (e.key === 'Escape') {
      closeSuggestions();
    }
  });

  function updateActiveOption(options) {
    options.forEach((el, idx) => {
      const selected = idx === activeIndex;
      el.setAttribute('aria-selected', selected ? 'true' : 'false');
      if (selected) el.scrollIntoView({ block: 'nearest' });
    });
  }

  document.getElementById('clear-search-btn').addEventListener('click', () => {
    searchInput.value = '';
    closeSuggestions();
    searchInput.focus();
  });

  [filterType, filterTown, filterLimit, filterLat, filterLon, filterProposed].forEach((el) => {
    el.addEventListener('change', () => {
      if (searchInput.value.trim().length >= 2) runAutosuggest();
    });
  });

  // Sample query chips
  document.querySelectorAll('[data-sample-q]').forEach((btn) => {
    btn.addEventListener('click', () => {
      searchInput.value = btn.getAttribute('data-sample-q');
      if (btn.getAttribute('data-sample-proposed') === 'true') {
        filterProposed.checked = true;
      }
      runAutosuggest();
    });
  });

  // Geo-bias chips
  document.querySelectorAll('[data-bias-lat]').forEach((btn) => {
    btn.addEventListener('click', () => {
      filterLat.value = btn.getAttribute('data-bias-lat');
      filterLon.value = btn.getAttribute('data-bias-lon');
      if (searchInput.value.trim().length >= 2) runAutosuggest();
    });
  });

  document.getElementById('clear-bias-btn').addEventListener('click', () => {
    filterLat.value = '';
    filterLon.value = '';
    if (searchInput.value.trim().length >= 2) runAutosuggest();
  });

  // Address ID form & chips
  document.getElementById('address-id-form').addEventListener('submit', (e) => {
    e.preventDefault();
    fetchAddressById(lookupIdInput.value);
  });

  document.querySelectorAll('[data-address-id]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const id = btn.getAttribute('data-address-id');
      lookupIdInput.value = id;
      fetchAddressById(id);
    });
  });

  // Reverse geocode form & chips
  document.getElementById('reverse-form').addEventListener('submit', (e) => {
    e.preventDefault();
    runReverseGeocode();
  });

  document.querySelectorAll('[data-rev-lat]').forEach((btn) => {
    btn.addEventListener('click', () => {
      revLatInput.value = btn.getAttribute('data-rev-lat');
      revLonInput.value = btn.getAttribute('data-rev-lon');
      runReverseGeocode();
    });
  });

  // Copy cURL button
  copyCurlBtn.addEventListener('click', async () => {
    if (!lastCurlCommand) return;
    try {
      await navigator.clipboard.writeText(lastCurlCommand);
      const prev = copyCurlBtn.textContent;
      copyCurlBtn.textContent = 'Copied!';
      setTimeout(() => { copyCurlBtn.textContent = prev; }, 1400);
    } catch (_) {}
  });

  // Close dropdown on outside click
  document.addEventListener('click', (e) => {
    if (!e.target.closest('.search-wrapper')) {
      closeSuggestions();
    }
  });

  // Initial health check on page load
  checkHealth(true);
})();
