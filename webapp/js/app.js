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
  commChart: null,
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

function createChartConfig(tooltipUnit, yAxisDecimals = 3) {
  const primaryColor = '#d946ef';
  const userColor = '#38bdf8';

  return {
    type: 'line',
    data: {
      labels: [],
      datasets: [
        {
          label: 'Valore',
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
              return `${context.dataset.label}: ${value.toFixed(yAxisDecimals === 0 ? 2 : 4)} ${tooltipUnit}`;
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
              return yAxisDecimals === 0 ? value.toFixed(0) : value.toFixed(3);
            },
          },
        },
      },
    },
  };
}

function initChart() {
  // Grafico tariffe energia
  const rateCtx = document.getElementById('rate-chart');
  if (rateCtx) {
    const rateUnit = state.service === 'luce' ? '€/kWh' : '€/Smc';
    state.chart = new Chart(rateCtx, createChartConfig(rateUnit, 3));
  }

  // Grafico commercializzazione
  const commCtx = document.getElementById('comm-chart');
  if (commCtx) {
    state.commChart = new Chart(commCtx, createChartConfig('€/anno', 0));
  }
}

function updateCharts(historyData) {
  const { labels, data, commercializzazione } = historyData;

  // Aggiorna grafico tariffe energia
  if (state.chart) {
    // Aggiorna label in base al tipo (variabile = spread, fissa = prezzo)
    const isVariabile = state.tipo === 'variabile';
    state.chart.data.datasets[0].label = isVariabile ? 'Spread su PUN/PSV' : 'Prezzo Energia';

    // Aggiorna dati principali
    state.chart.data.labels = labels;
    state.chart.data.datasets[0].data = data;

    // Aggiungi linea utente se disponibile
    const userRate = getUserCurrentRate();
    if (userRate !== null) {
      state.chart.data.datasets[1].data = labels.map(() => userRate);
      state.chart.data.datasets[1].hidden = false;
    } else {
      state.chart.data.datasets[1].data = [];
      state.chart.data.datasets[1].hidden = true;
    }

    state.chart.update();
  }

  // Aggiorna grafico commercializzazione
  if (state.commChart && commercializzazione) {
    state.commChart.data.datasets[0].label = 'Commercializzazione';
    state.commChart.data.labels = labels;
    state.commChart.data.datasets[0].data = commercializzazione;

    // Aggiungi linea utente commercializzazione se disponibile
    const userComm = getUserCurrentComm();
    if (userComm !== null) {
      state.commChart.data.datasets[1].data = labels.map(() => userComm);
      state.commChart.data.datasets[1].hidden = false;
    } else {
      state.commChart.data.datasets[1].data = [];
      state.commChart.data.datasets[1].hidden = true;
    }

    state.commChart.update();
  }
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

function getUserCurrentComm() {
  if (!state.userRates) return null;

  const serviceData = state.userRates[state.service];
  if (!serviceData) return null;

  // Verifica che tipo e fascia corrispondano
  if (serviceData.tipo !== state.tipo || serviceData.fascia !== state.fascia) {
    return null;
  }

  return serviceData.commercializzazione;
}

// ========== UI Helpers ==========

function showLoading() {
  const loading = document.getElementById('loading');
  const loadingComm = document.getElementById('loading-comm');
  const error = document.getElementById('error-message');
  const errorComm = document.getElementById('error-message-comm');

  loading.classList.remove('hidden');
  loadingComm.classList.remove('hidden');
  error.style.display = 'none';
  errorComm.style.display = 'none';
}

function hideLoading() {
  const loading = document.getElementById('loading');
  const loadingComm = document.getElementById('loading-comm');

  loading.classList.add('hidden');
  loadingComm.classList.add('hidden');
}

function showError(message) {
  const loading = document.getElementById('loading');
  const loadingComm = document.getElementById('loading-comm');
  const error = document.getElementById('error-message');
  const errorComm = document.getElementById('error-message-comm');
  const errorText = document.getElementById('error-text');
  const errorTextComm = document.getElementById('error-text-comm');

  loading.classList.add('hidden');
  loadingComm.classList.add('hidden');
  error.style.display = 'flex';
  errorComm.style.display = 'flex';
  errorText.textContent = message;
  errorTextComm.textContent = message;
}

function hideError() {
  const error = document.getElementById('error-message');
  const errorComm = document.getElementById('error-message-comm');

  error.style.display = 'none';
  errorComm.style.display = 'none';
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
    updateCharts(historyData);

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
    // Aggiorna fasce disponibili in base al tipo
    updateFasciaOptions();
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
  const isLuceFissa = state.service === 'luce' && state.tipo === 'fissa';
  const isLuceVariabile = state.service === 'luce' && state.tipo === 'variabile';

  // Gas e Luce Fissa hanno solo monoraria
  // Luce Variabile ha monoraria e trioraria (no bioraria con Octopus)
  if (isGas || isLuceFissa) {
    fasciaSelect.innerHTML = '<option value="monoraria">Monoraria</option>';
    state.fascia = 'monoraria';
  } else if (isLuceVariabile) {
    fasciaSelect.innerHTML = `
      <option value="monoraria">Monoraria</option>
      <option value="trioraria">Trioraria</option>
    `;
    // Reset fascia se era bioraria
    if (state.fascia === 'bioraria') {
      state.fascia = 'monoraria';
    }
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
