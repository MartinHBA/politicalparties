const express = require('express');
const path = require('path');
const fs = require('fs');
const { parse } = require('csv-parse/sync');

const app = express();
const PORT = process.env.PORT || 8080;

const ROOT_DIR = __dirname;
const STATIC_DIR = path.join(ROOT_DIR, 'static');
const INDEX_TEMPLATE_PATH = path.join(ROOT_DIR, 'index.html');
const RESULTS_PATH = path.join(ROOT_DIR, 'results.html');
const EXCLUSIONS_PATH = path.join(ROOT_DIR, 'exclusions.html');
const PERCENT_PATH = path.join(ROOT_DIR, 'percent.html');

const POLLS_FILE = path.join(ROOT_DIR, 'PollsSeats.csv');
const PARTY_COLORS_FILE = path.join(ROOT_DIR, 'PartyColors.csv');
const CREDIT_INFO_FILE = path.join(ROOT_DIR, 'creditInfo.csv');

const DEFAULT_COLOR = '#808080';
const DEFAULT_CREDIT = 'Zdroj neuvedeny';
const MAJORITY_TARGET = 76;

let partyColors = {};
let creditInfoMap = {};

const indexTemplate = fs.readFileSync(INDEX_TEMPLATE_PATH, 'utf8');

bootstrapReferenceData();

app.use(express.urlencoded({ extended: true, limit: '1mb' }));
app.use(express.json({ limit: '1mb' }));

app.use('/static', express.static(STATIC_DIR, { maxAge: '12h' }));

app.get('/', (req, res) => {
  try {
    const agencies = fetchAllAgencies();
    const html = renderIndexPage(agencies);
    res.send(html);
  } catch (err) {
    console.error('ERROR: Failed to render index.html', err);
    res.status(500).send('Internal Server Error');
  }
});

app.get('/results', (req, res) => {
  res.sendFile(RESULTS_PATH);
});

app.get('/exclusions', (req, res) => {
  res.sendFile(EXCLUSIONS_PATH);
});

app.get('/percent', (req, res) => {
  res.sendFile(PERCENT_PATH);
});

app.post('/submit', (req, res) => {
  try {
    const parties = parsePartiesPayload(req.body.parties);
    const chartData = buildChartData(parties, []);
    res.json(chartData);
  } catch (err) {
    console.error('ERROR: submit handler', err.message);
    const status = err.isClientError ? 400 : 500;
    res.status(status).json({ error: err.message });
  }
});

app.post('/submit_with_exclusions', (req, res) => {
  try {
    const parties = parsePartiesPayload(req.body.parties);
    const exclusionPairs = parseExclusionsPayload(req.body.exclusions);
    const chartData = buildChartData(parties, exclusionPairs);
    res.json(chartData);
  } catch (err) {
    console.error('ERROR: submit_with_exclusions handler', err.message);
    const status = err.isClientError ? 400 : 500;
    res.status(status).json({ error: err.message });
  }
});

app.get('/fetch', (req, res) => {
  const source = (req.query.source || '').trim();
  if (!source) {
    return res.status(400).json({ error: "Missing 'source' query parameter" });
  }

  const parts = source.split(' - ');
  if (parts.length !== 2) {
    return res
      .status(400)
      .json({ error: "Invalid 'source' format. Expected 'Agency Name - DD.MM.YYYY'" });
  }

  const [agency, date] = parts.map((value) => value.trim());
  console.log(`INFO: Fetch request for agency='${agency}', date='${date}'`);

  try {
    const { parties, actualDate, creditInfoText } = fetchAndFilterParties(agency, date);
    res.json({ parties, date: actualDate, creditInfo: creditInfoText });
  } catch (err) {
    console.error('ERROR: fetch handler failed', err);
    res.status(500).json({ error: 'Could not retrieve poll data' });
  }
});

app.use((req, res, next) => {
  if (req.method === 'GET') {
    const fallbackPath = path.join(ROOT_DIR, req.path);
    if (fs.existsSync(fallbackPath) && fs.statSync(fallbackPath).isFile()) {
      return res.sendFile(fallbackPath);
    }
  }
  next();
});

app.listen(PORT, () => {
  console.log(`Server listening on port ${PORT}`);
});

function bootstrapReferenceData() {
  try {
    partyColors = loadPartyColors();
    creditInfoMap = loadCreditInfo();
    console.log(
      `INFO: Loaded ${Object.keys(partyColors).length} party colors and ${Object.keys(creditInfoMap).length} credit entries.`,
    );
  } catch (err) {
    console.error('FATAL: Unable to load reference CSV files.', err);
    process.exit(1);
  }
}

function loadPartyColors() {
  const rows = readCsvFile(PARTY_COLORS_FILE);
  const map = {};
  rows.forEach((row) => {
    const name = (row.name || '').trim();
    const color = (row.color || '').trim();
    if (name) {
      map[name] = color || DEFAULT_COLOR;
    }
  });
  return map;
}

function loadCreditInfo() {
  const rows = readCsvFile(CREDIT_INFO_FILE);
  const map = {};
  rows.forEach((row) => {
    const id = (row.id || '').trim();
    const text = (row.text || '').trim();
    if (id) {
      map[id] = text || DEFAULT_CREDIT;
    }
  });
  return map;
}

function readCsvFile(filePath) {
  const fileContents = fs.readFileSync(filePath, 'utf8');
  return parse(fileContents, {
    columns: true,
    skip_empty_lines: true,
    trim: true,
    relax_column_count: true,
  });
}

function fetchAllAgencies() {
  const rows = readCsvFile(POLLS_FILE);
  const agencyMap = new Map();

  rows.forEach((row) => {
    const dateStr = (row.date || '').trim();
    const agencyName = (row.agency || '').trim();
    if (!dateStr || !agencyName) {
      return;
    }
    const label = `${agencyName} - ${dateStr}`;
    if (!agencyMap.has(label)) {
      agencyMap.set(label, parseDate(dateStr));
    }
  });

  return Array.from(agencyMap.entries())
    .sort((a, b) => b[1] - a[1])
    .map(([label]) => label);
}

function parseDate(value) {
  const parts = value.split('.');
  if (parts.length !== 3) {
    return new Date(0);
  }
  const [day, month, year] = parts.map((part) => parseInt(part, 10));
  if ([day, month, year].some((num) => Number.isNaN(num))) {
    return new Date(0);
  }
  return new Date(year, month - 1, day);
}

function renderIndexPage(agencies) {
  const placeholderRegex = /{{range \.}}[\s\S]*?{{end}}/;
  const optionsMarkup = agencies
    .map((agency) => {
      const escaped = escapeHtml(agency);
      return `                        <option value="${escaped}">${escaped}</option>`;
    })
    .join('\n');

  if (!placeholderRegex.test(indexTemplate)) {
    return indexTemplate;
  }

  const replacement = agencies.length ? `\n${optionsMarkup}\n                    ` : '\n';
  return indexTemplate.replace(placeholderRegex, replacement);
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function fetchAndFilterParties(agencyFilter, dateFilter) {
  const rows = readCsvFile(POLLS_FILE);
  const parties = [];
  let actualDate = '';
  let creditInfoId = '';

  rows.forEach((row, index) => {
    const rowDate = (row.date || '').trim();
    const rowAgency = (row.agency || '').trim();
    if (rowAgency !== agencyFilter || rowDate !== dateFilter) {
      return;
    }

    if (!actualDate) {
      actualDate = rowDate;
      creditInfoId = (row.creditInfoId || '').trim();
    }

    const partyName = (row.name || '').trim();
    const seatStr = (row.seat || '').trim();
    const seats = parseInt(seatStr, 10);
    if (!partyName || Number.isNaN(seats) || seats <= 0) {
      console.warn(
        `WARN: Skipping invalid row ${index + 2} for agency='${rowAgency}', date='${rowDate}'.`,
      );
      return;
    }

    parties.push({
      name: partyName,
      seats,
      color: partyColors[partyName] || DEFAULT_COLOR,
    });
  });

  const creditInfoText =
    creditInfoId && creditInfoMap[creditInfoId] ? creditInfoMap[creditInfoId] : DEFAULT_CREDIT;

  return { parties, actualDate, creditInfoText };
}

function parsePartiesPayload(rawValue) {
  if (!rawValue) {
    const error = new Error("Missing 'parties' data");
    error.isClientError = true;
    throw error;
  }

  let parsed;
  try {
    parsed = JSON.parse(rawValue);
  } catch (err) {
    const error = new Error("Invalid 'parties' JSON payload");
    error.isClientError = true;
    throw error;
  }

  if (!Array.isArray(parsed)) {
    const error = new Error("'parties' JSON must be an array");
    error.isClientError = true;
    throw error;
  }

  const parties = parsed
    .map((entry) => {
      if (!entry) {
        return null;
      }
      const name = (entry.name || '').trim();
      const seats = parseInt(entry.seats, 10);
      const color = (entry.color || DEFAULT_COLOR).trim() || DEFAULT_COLOR;
      if (!name || Number.isNaN(seats) || seats <= 0) {
        return null;
      }
      return { name, seats, color };
    })
    .filter(Boolean);

  return parties;
}

function parseExclusionsPayload(rawValue) {
  if (!rawValue) {
    const error = new Error("Missing 'exclusions' data");
    error.isClientError = true;
    throw error;
  }

  let parsed;
  try {
    parsed = JSON.parse(rawValue);
  } catch (err) {
    const error = new Error("Invalid 'exclusions' JSON payload");
    error.isClientError = true;
    throw error;
  }

  if (!Array.isArray(parsed)) {
    const error = new Error("'exclusions' JSON must be an array");
    error.isClientError = true;
    throw error;
  }

  return parsed
    .map((entry) => {
      if (!entry) {
        return null;
      }
      const firstParty = (entry.firstParty || entry.FirstParty || '').trim();
      const secondParty = (entry.secondParty || entry.SecondParty || '').trim();
      if (!firstParty || !secondParty || firstParty === secondParty) {
        return null;
      }
      return { firstParty, secondParty };
    })
    .filter(Boolean);
}

function buildChartData(parties, exclusionPairs) {
  if (!parties.length) {
    return [];
  }

  const combinations = findCombinations(parties, MAJORITY_TARGET, exclusionPairs);
  const chartData = combinations.map((combo) => {
    const labels = combo.map((party) => party.name);
    const values = combo.map((party) => party.seats);
    const colors = combo.map((party) => party.color || DEFAULT_COLOR);
    const totalSeats = values.reduce((sum, seatCount) => sum + seatCount, 0);
    return { labels, values, colors, totalSeats };
  });

  chartData.sort((a, b) => {
    if (a.labels.length !== b.labels.length) {
      return a.labels.length - b.labels.length;
    }
    return a.totalSeats - b.totalSeats;
  });

  return chartData;
}

function findCombinations(parties, target, exclusionPairs) {
  const result = [];

  function dfs(index, current, sum) {
    if (sum >= target) {
      if (!containsExcludedPair(current, exclusionPairs)) {
        result.push([...current]);
      }
      return;
    }

    if (index >= parties.length) {
      return;
    }

    current.push(parties[index]);
    dfs(index + 1, current, sum + parties[index].seats);
    current.pop();
    dfs(index + 1, current, sum);
  }

  dfs(0, [], 0);
  return result;
}

function containsExcludedPair(combination, exclusionPairs) {
  if (!exclusionPairs || exclusionPairs.length === 0) {
    return false;
  }
  const names = new Set(combination.map((party) => party.name));
  return exclusionPairs.some(
    (pair) => names.has(pair.firstParty) && names.has(pair.secondParty),
  );
}
