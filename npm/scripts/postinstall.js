#!/usr/bin/env node

// Skip postinstall output in CI or when suppressed
if (process.env.CI || process.env.npm_config_yes || process.env.VIBECODING_SKIP_POSTINSTALL) {
  process.exit(0);
}

const os = require('os');
const path = require('path');

const RESET  = '\x1b[0m';
const BOLD   = '\x1b[1m';
const DIM    = '\x1b[2m';
const CYAN   = '\x1b[36m';
const BRIGHT_GREEN = '\x1b[92m';
const WHITE  = '\x1b[97m';
const YELLOW = '\x1b[33m';

const logo = [
  '██   ██  ███  ████ █  █ █  █',
  '███ ███ █   █  ██  █  █  ██ ',
  '█ ███ █ █   █  ██  ████  ██ ',
  '█  █  █ █   █  ██  █  █ █  █',
  '█     █  ███   ██  █  █ █  █',
].join('\n');

function configPath() {
  if (process.platform === 'win32') {
    const appData = process.env.APPDATA || path.join(os.homedir(), 'AppData', 'Roaming');
    return path.join(appData, 'mothx', 'settings.json');
  }
  return path.join(os.homedir(), '.mothx', 'settings.json');
}

function pkgVersion() {
  try {
    return require('../package.json').version;
  } catch {
    return '';
  }
}

function pkgName() {
  try {
    return require('../package.json').name;
  } catch {
    return '';
  }
}

const ver = pkgVersion();
const verStr = ver ? ` ${DIM}v${ver}${RESET}` : '';
const name = pkgName();
const legacy = name === 'vibecoding-installer';
const command = legacy ? 'vibecoding' : 'mothx';

console.log();
console.log(`${BRIGHT_GREEN}${BOLD}${logo}${RESET}${verStr}`);
console.log();
if (legacy) {
  console.log(`  ${YELLOW}${BOLD}vibecoding-installer is a compatibility package.${RESET}`);
  console.log(`  ${YELLOW}Future updates move to: npm install -g mothx-installer@latest${RESET}`);
  console.log();
}
console.log(`  ${BOLD}${WHITE}Your AI pair programmer, right in the terminal.${RESET}`);
console.log();
console.log(`  ${DIM}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}`);
console.log();
console.log(`  ${BOLD}Quick Start${RESET}`);
console.log();
console.log(`    ${command}                          ${DIM}Interactive mode${RESET}`);
console.log(`    ${command} -P "write fizzbuzz in Go" ${DIM}One-shot mode${RESET}`);
console.log();
console.log(`  ${BOLD}Setup${RESET}`);
console.log();
console.log(`    In TUI, type ${CYAN}${BOLD}/auth${RESET} to add API keys and switch providers`);
console.log();
console.log(`  ${BOLD}Docs${RESET}   ${CYAN}https://mothx.net/${RESET}`);
console.log(`  ${BOLD}Code${RESET}   ${CYAN}https://github.com/oschina/mothx${RESET}`);
console.log(`  ${BOLD}Config${RESET} ${DIM}${configPath()}${RESET}`);
console.log();
