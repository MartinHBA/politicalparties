# Political Parties Coalition Calculator

This repository now runs on **Node.js 24 LTS** using Express and the original front-end assets.

## Getting Started

```bash
npm install
npm start        # or PORT=3000 npm start
```

The server loads data from the CSV files in the project root and serves:

- `/` – main calculator (renders `index.html` with poll dropdown options)
- `/fetch` – REST endpoint returning poll data for the selected agency/date
- `/submit` and `/submit_with_exclusions` – coalition combination calculators
- `/results`, `/exclusions`, `/percent` – auxiliary pages
- `/static/*` – static assets (images, CSV with percentages, etc.)

Deploying to Azure Web App only requires setting the Node version to 24 LTS (or higher) and using the default `npm start` command.
