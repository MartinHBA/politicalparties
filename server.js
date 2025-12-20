const express = require('express');
const path = require('path');
const fs = require('fs');
const crypto = require('crypto');
const { parse } = require('csv-parse/sync');
const { DefaultAzureCredential } = require('@azure/identity');
const { TableClient } = require('@azure/data-tables');
const { EmailClient } = require('@azure/communication-email');

const app = express();
app.set('trust proxy', true);
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
const SUBSCRIBERS_PARTITION_KEY = 'subscribers';
const EMAIL_REGEX = /^[^\s@]+@[^\s@]+\.[^\s@]+$/i;
const RATE_LIMIT_WINDOW_MS = 10 * 60 * 1000; // 10 minutes
const RATE_LIMIT_MAX_REQUESTS = 5;

const newsletterConfig = {
  tableServiceUrl: process.env.TABLE_SERVICE_URL,
  tableName: process.env.TABLE_NAME || 'Subscribers',
  siteBaseUrl: process.env.SITE_BASE_URL,
  acsConnectionString: process.env.ACS_EMAIL_CONNECTION_STRING,
  acsFrom: process.env.ACS_EMAIL_FROM,
  adminApiKey: process.env.ADMIN_API_KEY,
};

let tableCredential;
let tableClientInstance;
let emailClientInstance;
const rateLimitCache = new Map();

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

app.post('/api/subscribe', async (req, res) => {
  const clientIp = getClientIp(req);
  if (isRateLimited(clientIp)) {
    return res.status(429).json({ error: 'Príliš veľa pokusov. Skúste to znova neskôr.' });
  }

  const honeypot = typeof req.body?.website === 'string' ? req.body.website.trim() : '';
  if (honeypot) {
    return res.json({ status: 'ok' });
  }

  const emailValue = typeof req.body?.email === 'string' ? req.body.email.trim() : '';
  const consentGiven = req.body?.consent === true;

  const normalizedEmail = normalizeEmail(emailValue);
  if (!normalizedEmail || !EMAIL_REGEX.test(normalizedEmail)) {
    return res.status(400).json({ error: 'Zadajte platný email.' });
  }
  if (!consentGiven) {
    return res.status(400).json({ error: 'S odberom musíte súhlasiť.' });
  }

  try {
    const result = await subscribeEmail(normalizedEmail);
    const status = result.alreadyActive ? 'already_subscribed' : 'subscribed';
    res.json({ status });
  } catch (err) {
    console.error('ERROR: subscribe handler failed', err);
    res.status(500).json({ error: 'Nepodarilo sa uložiť odber.' });
  }
});

app.get('/api/unsubscribe', async (req, res) => {
  const token = typeof req.query.token === 'string' ? req.query.token.trim() : '';
  if (!token) {
    return res.status(400).send(renderUnsubscribeMessage('Chýba token odhlásenia.'));
  }

  try {
    const subscriber = await findSubscriberByToken(token);
    if (!subscriber) {
      return res
        .status(404)
        .send(renderUnsubscribeMessage('Odber sme nenašli. Možno už bol zrušený.'));
    }
    if ((subscriber.Status || '').toLowerCase() === 'unsubscribed') {
      return res.send(renderUnsubscribeMessage('Odber už bol zrušený.'));
    }

    await markSubscriberAsUnsubscribed(subscriber);
    res.send(renderUnsubscribeMessage('Odber bol úspešne zrušený.'));
  } catch (err) {
    console.error('ERROR: unsubscribe handler failed', err);
    res.status(500).send(renderUnsubscribeMessage('Nepodarilo sa spracovať odhlásenie.'));
  }
});

app.post('/api/send-update', async (req, res) => {
  const adminKey = req.get('x-admin-key');
  if (!newsletterConfig.adminApiKey) {
    console.error('ERROR: ADMIN_API_KEY is not configured.');
    return res.status(500).json({ error: 'Server je nesprávne nakonfigurovaný.' });
  }
  if (!adminKey || adminKey !== newsletterConfig.adminApiKey) {
    return res.status(401).json({ error: 'Neoprávnený prístup.' });
  }

  try {
    const activeSubscribers = await listActiveSubscribers();
    if (!activeSubscribers.length) {
      return res.json({ attempted: 0, sent: 0, failed: 0 });
    }

    const emailClient = getEmailClient();
    const siteUrl = getSiteBaseUrl();
    const { updateId } = req.body || {};
    console.log(
      `INFO: send-update triggered (updateId=${updateId || 'n/a'}) for ${activeSubscribers.length} subscribers.`,
    );

    let attempted = 0;
    let sent = 0;
    let failed = 0;

    for (const subscriber of activeSubscribers) {
      attempted += 1;
      try {
        await sendUpdateEmailToSubscriber(subscriber, emailClient, siteUrl);
        sent += 1;
      } catch (err) {
        failed += 1;
        console.error(`ERROR: Failed to send update to ${subscriber.Email}`, err);
      }
    }

    res.json({ updateId: updateId || null, attempted, sent, failed });
  } catch (err) {
    console.error('ERROR: send-update handler failed', err);
    res.status(500).json({ error: 'Nepodarilo sa odoslať emaily.' });
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

function getClientIp(req) {
  return (
    (req.headers['x-forwarded-for'] || '').split(',')[0].trim() ||
    req.ip ||
    req.connection?.remoteAddress ||
    'unknown'
  );
}

function isRateLimited(ip) {
  if (!ip) {
    return false;
  }
  const now = Date.now();
  const entry = rateLimitCache.get(ip);
  if (!entry || now - entry.windowStart > RATE_LIMIT_WINDOW_MS) {
    rateLimitCache.set(ip, { count: 1, windowStart: now });
    return false;
  }
  entry.count += 1;
  if (entry.count > RATE_LIMIT_MAX_REQUESTS) {
    return true;
  }
  if (rateLimitCache.size > 1000) {
    for (const [cachedIp, data] of rateLimitCache.entries()) {
      if (now - data.windowStart > RATE_LIMIT_WINDOW_MS) {
        rateLimitCache.delete(cachedIp);
      }
    }
  }
  return false;
}

function normalizeEmail(value) {
  return (value || '').trim().toLowerCase();
}

function getTableClient() {
  if (!newsletterConfig.tableServiceUrl) {
    throw new Error('TABLE_SERVICE_URL is not configured.');
  }
  if (!newsletterConfig.tableName) {
    throw new Error('TABLE_NAME is not configured.');
  }
  if (!tableCredential) {
    tableCredential = new DefaultAzureCredential();
  }
  if (!tableClientInstance) {
    tableClientInstance = new TableClient(
      newsletterConfig.tableServiceUrl,
      newsletterConfig.tableName,
      tableCredential,
    );
  }
  return tableClientInstance;
}

function getEmailClient() {
  if (!newsletterConfig.acsConnectionString) {
    throw new Error('ACS_EMAIL_CONNECTION_STRING is not configured.');
  }
  if (!newsletterConfig.acsFrom) {
    throw new Error('ACS_EMAIL_FROM is not configured.');
  }
  if (!emailClientInstance) {
    emailClientInstance = new EmailClient(newsletterConfig.acsConnectionString);
  }
  return emailClientInstance;
}

function getSiteBaseUrl() {
  const base = (newsletterConfig.siteBaseUrl || '').trim();
  if (!base) {
    throw new Error('SITE_BASE_URL is not configured.');
  }
  return base.endsWith('/') ? base.slice(0, -1) : base;
}

async function subscribeEmail(normalizedEmail) {
  const existing = await getSubscriberEntity(normalizedEmail);
  if (existing && (existing.Status || '').toLowerCase() === 'active') {
    return { alreadyActive: true };
  }
  const nowIso = new Date().toISOString();
  const entity = buildSubscriberEntity({
    email: normalizedEmail,
    status: 'active',
    consentGiven: true,
    createdAt: existing?.CreatedAtUtc || nowIso,
    unsubscribeToken: existing?.UnsubscribeToken || crypto.randomUUID(),
    unsubscribedAt: '',
  });
  await getTableClient().upsertEntity(entity, 'Replace');
  return { alreadyActive: false };
}

async function getSubscriberEntity(email) {
  if (!email) {
    return null;
  }
  try {
    return await getTableClient().getEntity(SUBSCRIBERS_PARTITION_KEY, email);
  } catch (err) {
    if (isEntityNotFoundError(err)) {
      return null;
    }
    throw err;
  }
}

function isEntityNotFoundError(err) {
  return (
    err?.statusCode === 404 ||
    err?.code === 'ResourceNotFound' ||
    err?.code === 'EntityNotFound'
  );
}

async function findSubscriberByToken(token) {
  const client = getTableClient();
  const filterToken = escapeOdataValue(token);
  const filter = `PartitionKey eq '${SUBSCRIBERS_PARTITION_KEY}' and UnsubscribeToken eq '${filterToken}'`;
  const iterator = client.listEntities({
    queryOptions: { filter },
  });
  for await (const entity of iterator) {
    return entity;
  }
  return null;
}

function escapeOdataValue(value) {
  return (value || '').replace(/'/g, "''");
}

async function markSubscriberAsUnsubscribed(subscriber) {
  const nowIso = new Date().toISOString();
  const rowKey = getSubscriberRowKey(subscriber);
  const normalizedEmail = normalizeEmail(subscriber.Email || rowKey || '');
  const email = normalizedEmail || rowKey;
  if (!email) {
    throw new Error('Subscriber entity is missing an email.');
  }
  const entity = buildSubscriberEntity({
    email,
    status: 'unsubscribed',
    consentGiven: !!subscriber.ConsentGiven,
    createdAt: subscriber.CreatedAtUtc || nowIso,
    unsubscribeToken: subscriber.UnsubscribeToken || crypto.randomUUID(),
    unsubscribedAt: nowIso,
  });
  await getTableClient().upsertEntity(entity, 'Replace');
}

function buildSubscriberEntity({
  email,
  status,
  consentGiven,
  createdAt,
  unsubscribeToken,
  unsubscribedAt,
}) {
  if (!email) {
    throw new Error('Missing subscriber email.');
  }
  return {
    partitionKey: SUBSCRIBERS_PARTITION_KEY,
    rowKey: email,
    Email: email,
    Status: status,
    ConsentGiven: consentGiven,
    CreatedAtUtc: createdAt,
    UnsubscribeToken: unsubscribeToken,
    UnsubscribedAtUtc: unsubscribedAt || '',
  };
}

function getSubscriberRowKey(subscriber) {
  return subscriber?.rowKey || subscriber?.RowKey || normalizeEmail(subscriber?.Email);
}

async function listActiveSubscribers() {
  const client = getTableClient();
  const filter = `PartitionKey eq '${SUBSCRIBERS_PARTITION_KEY}' and Status eq 'active'`;
  const subscribers = [];
  for await (const entity of client.listEntities({ queryOptions: { filter } })) {
    subscribers.push(entity);
  }
  return subscribers;
}

async function sendUpdateEmailToSubscriber(subscriber, emailClient, siteUrl) {
  const recipient = normalizeEmail(subscriber.Email || getSubscriberRowKey(subscriber) || '');
  if (!recipient) {
    throw new Error('Subscriber entity has no email.');
  }
  const token = await ensureSubscriberToken(subscriber);
  const unsubscribeUrl = buildUnsubscribeUrl(siteUrl, token);
  const message = {
    senderAddress: newsletterConfig.acsFrom,
    recipients: { to: [{ address: recipient }] },
    content: {
      subject: 'Aktualizácia stránky',
      plainText: buildUpdateEmailBody(siteUrl, unsubscribeUrl),
    },
  };
  const poller = await emailClient.beginSend(message);
  await poller.pollUntilDone();
}

function buildUpdateEmailBody(siteUrl, unsubscribeUrl) {
  return [
    'Na stránke pribudli nové prieskumy politických strán.',
    '',
    `Pozrieť: ${siteUrl}`,
    '',
    `Odhlásiť odber: ${unsubscribeUrl}`,
  ].join('\n');
}

function buildUnsubscribeUrl(siteUrl, token) {
  return `${siteUrl}/api/unsubscribe?token=${encodeURIComponent(token)}`;
}

async function ensureSubscriberToken(subscriber) {
  if (subscriber.UnsubscribeToken) {
    return subscriber.UnsubscribeToken;
  }
  const newToken = crypto.randomUUID();
  const rowKey = getSubscriberRowKey(subscriber);
  if (!rowKey) {
    throw new Error('Subscriber entity is missing a row key.');
  }
  await getTableClient().updateEntity(
    {
      partitionKey: SUBSCRIBERS_PARTITION_KEY,
      rowKey,
      UnsubscribeToken: newToken,
    },
    'Merge',
  );
  subscriber.UnsubscribeToken = newToken;
  return newToken;
}

function renderUnsubscribeMessage(message) {
  return `<!DOCTYPE html>
<html lang="sk">
<head>
<meta charset="utf-8" />
<title>Odhlásenie z emailov</title>
<style>
  body { font-family: Arial, sans-serif; background-color: #f6f6f6; margin: 0; padding: 0; }
  .wrapper { max-width: 480px; margin: 10vh auto; background: #ffffff; padding: 32px; border-radius: 8px; box-shadow: 0 4px 16px rgba(0,0,0,0.08); text-align: center; }
  h1 { font-size: 1.4rem; margin-bottom: 1rem; color: #222; }
  p { color: #444; font-size: 1rem; margin: 0; }
</style>
</head>
<body>
  <div class="wrapper">
    <h1>Aktualizácie webu</h1>
    <p>${message}</p>
  </div>
</body>
</html>`;
}
