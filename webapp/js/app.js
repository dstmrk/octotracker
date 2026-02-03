/**
 * OctoTracker Mini App - Main Application
 *
 * Gestisce:
 * - Integrazione Telegram WebApp
 * - Fetch dati API
 * - Rendering grafico Chart.js
 * - Interazione utente (tab, filtri, periodo)
 */

// ========== Configurazione ==========

const CONFIG = {
  // API base URL (relativo, stesso host)
  apiBaseUrl: '/api',
  // Default selections
  defaultService: 'luce',
  defaultTipo: 'variabile',
  defaultFascia: 'monoraria',
  defaultDays: 365,
};

// ========== State ==========

const state = {
  service: CONFIG.defaultService,
  tipo: CONFIG.defaultTipo,
  fascia: CONFIG.defaultFascia,
  days: CONFIG.defaultDays,
  chart: null,
  userRates: null,
  currentRates: null,
};

// ========== Telegram WebApp ==========

const tg = window.Telegram?.WebApp;

function initTelegram() {
  if (!tg) {
    console.warn('Telegram WebApp not available');
    return;
  }

  // Espandi la Mini App
  tg.expand();

  // Imposta colore header
  tg.setHeaderColor('secondary_bg_color');

  // Abilita pulsante chiusura
  tg.enableClosingConfirmation();

  // Ready
  tg.ready();

  console.log('Telegram WebApp initialized', {
    initData: tg.initData ? 'present' : 'missing',
    user: tg.initDataUnsafe?.user?.id,
  });
}

function getInitData() {
  return tg?.initData || '';
}

// ========== API ==========

async function fetchAPI(endpoint, params = {}) {
  const url = new URL(CONFIG.apiBaseUrl + endpoint, window.location.origin);

  // Aggiungi query params
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null) {
      url.searchParams.set(key, value);
    }
  });

  const initData = getInitData();
  if (!initData) {
    throw new Error('Autenticazione Telegram non disponibile');
  }

  const response = await fetch(url.toString(), {
    method: 'GET',
    headers: {
      'X-Telegram-Init-Data': initData,
      'Content-Type': 'application/json',
    },
  });

  const data = await response.json();

  if (!response.ok || !data.success) {
    throw new Error(data.error || `HTTP ${response.status}`);
  }

  return data.data;
}

async function fetchRateHistory() {
  return fetchAPI('/rates/history', {
    servizio: state.service,
    tipo: state.tipo,
    fascia: state.fascia,
    days: state.days,
  });
}

async function fetchUserRates() {
  return fetchAPI('/user/rates');
}

async function fetchCurrentRates() {
  return fetchAPI('/rates/current');
}

// ========== Chart ==========

function initChart() {
  const ctx = document.getElementById('rate-chart');
  if (!ctx) return;

  // Colori dal CSS
  const primaryColor = '#d946ef';
  const userColor = '#38bdf8';

  state.chart = new Chart(ctx, {
    type: 'line',
    data: {
      labels: [],
      datasets: [
        {
          label: 'Prezzo Energia',
          data: [],
          borderColor: primaryColor,
          backgroundColor: 'rgba(217, 70, 239, 0.1)',
          borderWidth: 2,
          fill: true,
          tension: 0.3,
          pointRadius: 0,
          pointHoverRadius: 4,
        },
        {
          label: 'Tua tariffa',
          data: [],
          borderColor: userColor,
          borderWidth: 2,
          borderDash: [5, 5],
          fill: false,
          tension: 0,
          pointRadius: 0,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: {
        intersect: false,
        mode: 'index',
      },
      plugins: {
        legend: {
          display: true,
          position: 'bottom',
          labels: {
            boxWidth: 12,
            padding: 8,
            font: {
              size: 10,
              family: "'Inter', sans-serif",
            },
          },
        },
        tooltip: {
          backgroundColor: '#0f172a',
          titleFont: {
            family: "'Inter', sans-serif",
            size: 11,
          },
          bodyFont: {
            family: "'JetBrains Mono', monospace",
            size: 12,
          },
          padding: 10,
          cornerRadius: 8,
          displayColors: true,
          callbacks: {
            label: function (context) {
              const value = context.parsed.y;
              const unit = state.service === 'luce' ? '€/kWh' : '€/Smc';
              return `${context.dataset.label}: ${value.toFixed(4)} ${unit}`;
            },
          },
        },
      },
      scales: {
        x: {
          display: true,
          grid: {
            display: false,
          },
          ticks: {
            maxTicksLimit: 6,
            font: {
              size: 9,
              family: "'Inter', sans-serif",
            },
            callback: function (value, index) {
              const label = this.getLabelForValue(value);
              // Formatta data in modo compatto
              const date = new Date(label);
              return date.toLocaleDateString('it-IT', {
                day: '2-digit',
                month: 'short',
              });
            },
          },
        },
        y: {
          display: true,
          grid: {
            color: 'rgba(0, 0, 0, 0.05)',
          },
          ticks: {
            font: {
              size: 9,
              family: "'JetBrains Mono', monospace",
            },
            callback: function (value) {
              return value.toFixed(3);
            },
          },
        },
      },
    },
  });
}

function updateChart(historyData) {
  if (!state.chart) return;

  const { labels, data } = historyData;

  // Aggiorna dati principali
  state.chart.data.labels = labels;
  state.chart.data.datasets[0].data = data;

  // Aggiungi linea utente se disponibile
  const userRate = getUserCurrentRate();
  if (userRate !== null) {
    // Crea array con stesso valore per tutta la lunghezza
    state.chart.data.datasets[1].data = labels.map(() => userRate);
    state.chart.data.datasets[1].hidden = false;
  } else {
    state.chart.data.datasets[1].data = [];
    state.chart.data.datasets[1].hidden = true;
  }

  state.chart.update();
}

// ========== Stats ==========

function getUserCurrentRate() {
  if (!state.userRates) return null;

  const serviceData = state.userRates[state.service];
  if (!serviceData) return null;

  // Verifica che tipo e fascia corrispondano
  if (serviceData.tipo !== state.tipo || serviceData.fascia !== state.fascia) {
    return null;
  }

  return serviceData.energia;
}

function updateStats(historyData) {
  const statsCard = document.getElementById('stats-card');
  const statCurrent = document.getElementById('stat-current');
  const statUser = document.getElementById('stat-user');
  const statMinMax = document.getElementById('stat-minmax');

  if (!historyData || !historyData.data || historyData.data.length === 0) {
    statsCard.style.display = 'none';
    return;
  }

  statsCard.style.display = 'grid';

  const unit = state.service === 'luce' ? '€/kWh' : '€/Smc';
  const lastValue = historyData.data[historyData.data.length - 1];

  // Valore corrente
  statCurrent.textContent = `${lastValue.toFixed(4)} ${unit}`;

  // Tariffa utente
  const userRate = getUserCurrentRate();
  if (userRate !== null) {
    statUser.textContent = `${userRate.toFixed(4)} ${unit}`;
  } else {
    statUser.textContent = '-';
  }

  // Min/Max
  if (historyData.stats) {
    const { min, max } = historyData.stats;
    statMinMax.textContent = `${min.toFixed(4)} / ${max.toFixed(4)}`;
  } else {
    const min = Math.min(...historyData.data);
    const max = Math.max(...historyData.data);
    statMinMax.textContent = `${min.toFixed(4)} / ${max.toFixed(4)}`;
  }
}

// ========== UI Helpers ==========

function showLoading() {
  const loading = document.getElementById('loading');
  const error = document.getElementById('error-message');
  loading.classList.remove('hidden');
  error.style.display = 'none';
}

function hideLoading() {
  const loading = document.getElementById('loading');
  loading.classList.add('hidden');
}

function showError(message) {
  const loading = document.getElementById('loading');
  const error = document.getElementById('error-message');
  const errorText = document.getElementById('error-text');

  loading.classList.add('hidden');
  error.style.display = 'flex';
  errorText.textContent = message;
}

function hideError() {
  const error = document.getElementById('error-message');
  error.style.display = 'none';
}

// ========== Data Loading ==========

async function loadData() {
  showLoading();
  hideError();

  try {
    // Carica dati in parallelo
    const [historyData, userRates] = await Promise.all([
      fetchRateHistory(),
      state.userRates ? Promise.resolve(state.userRates) : fetchUserRates().catch(() => null),
    ]);

    // Salva dati utente per riutilizzo
    if (!state.userRates && userRates) {
      state.userRates = userRates;
    }

    // Aggiorna UI
    updateChart(historyData);
    updateStats(historyData);

    hideLoading();
  } catch (error) {
    console.error('Error loading data:', error);
    showError(error.message || 'Errore nel caricamento dei dati');
  }
}

// ========== Event Handlers ==========

function setupEventListeners() {
  // Tab buttons (Luce/Gas)
  document.querySelectorAll('.tab-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      const service = btn.dataset.service;
      if (service === state.service) return;

      // Update state
      state.service = service;

      // Update UI
      document.querySelectorAll('.tab-btn').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');

      // Aggiorna fascia disponibili per gas (solo monoraria)
      updateFasciaOptions();

      // Reload data
      loadData();
    });
  });

  // Tipo select
  document.getElementById('tipo-select').addEventListener('change', (e) => {
    state.tipo = e.target.value;
    loadData();
  });

  // Fascia select
  document.getElementById('fascia-select').addEventListener('change', (e) => {
    state.fascia = e.target.value;
    loadData();
  });

  // Period buttons
  document.querySelectorAll('.period-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      const days = parseInt(btn.dataset.days, 10);
      if (days === state.days) return;

      // Update state
      state.days = days;

      // Update UI
      document.querySelectorAll('.period-btn').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');

      // Reload data
      loadData();
    });
  });
}

function updateFasciaOptions() {
  const fasciaSelect = document.getElementById('fascia-select');
  const isGas = state.service === 'gas';

  // Gas ha solo monoraria
  if (isGas) {
    fasciaSelect.innerHTML = '<option value="monoraria">Monoraria</option>';
    state.fascia = 'monoraria';
  } else {
    fasciaSelect.innerHTML = `
      <option value="monoraria">Monoraria</option>
      <option value="bioraria">Bioraria</option>
      <option value="trioraria">Trioraria</option>
    `;
  }

  fasciaSelect.value = state.fascia;
}

// ========== Initialization ==========

function init() {
  console.log('OctoTracker Mini App initializing...');

  // Init Telegram
  initTelegram();

  // Init Chart
  initChart();

  // Setup event listeners
  setupEventListeners();

  // Load initial data
  loadData();

  console.log('OctoTracker Mini App ready');
}

// Start app when DOM ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
