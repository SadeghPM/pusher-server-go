'use strict';

const { execFileSync, spawn } = require('node:child_process');
const { once } = require('node:events');
const fs = require('node:fs');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

const { createBackend } = require('./rest');

const ROOT = path.resolve(__dirname, '..', '..');

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function getFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      server.close((err) => (err ? reject(err) : resolve(port)));
    });
  });
}

function waitForPort(port, timeout = 15000) {
  const deadline = Date.now() + timeout;

  return new Promise((resolve, reject) => {
    const tryConnect = () => {
      const socket = net.connect({ port, host: '127.0.0.1' });
      let settled = false;

      const finish = (ok) => {
        if (settled) {
          return;
        }
        settled = true;
        socket.destroy();
        if (ok) {
          resolve();
        } else if (Date.now() >= deadline) {
          reject(new Error(`Timed out waiting for 127.0.0.1:${port}`));
        } else {
          setTimeout(tryConnect, 50);
        }
      };

      socket.setTimeout(500);
      socket.once('connect', () => finish(true));
      socket.once('timeout', () => finish(false));
      socket.once('error', () => finish(false));
    };

    tryConnect();
  });
}

function buildBinary(destination) {
  execFileSync('go', ['build', '-o', destination, '.'], {
    cwd: ROOT,
    stdio: 'inherit',
  });
}

function configYaml({ port, metricsPort, dashboardPort, webhookUrl }) {
  const webhooks = webhookUrl ? `["${webhookUrl}"]` : '[]';

  return `port: "${port}"
metrics_port: "${metricsPort}"
dashboard_port: "${dashboardPort}"
host: "http://127.0.0.1:${port}"
admin_token: "compat-admin-token"
debug: false
apps:
  - app_id: "compat-app"
    app_key: "compat-key"
    app_secret: "compat-secret"
    allowed_origins: ["*"]
    webhooks: ${webhooks}
  - app_id: "origin-app"
    app_key: "origin-key"
    app_secret: "origin-secret"
    allowed_origins: ["http://allowed.example"]
    webhooks: []
  - app_id: "other-app"
    app_key: "other-key"
    app_secret: "other-secret"
    allowed_origins: ["*"]
    webhooks: []
  - app_id: "3"
    app_key: "278d425bdf160c739803"
    app_secret: "7ad3773142a6692b25b8"
    allowed_origins: ["*"]
    webhooks: []
`;
}

async function startServer({ webhookUrl } = {}) {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pusher-go-compat-'));
  const binary = path.join(
    tmpDir,
    process.platform === 'win32' ? 'pusher-server.exe' : 'pusher-server',
  );

  buildBinary(binary);

  const [port, metricsPort, dashboardPort] = await Promise.all([
    getFreePort(),
    getFreePort(),
    getFreePort(),
  ]);

  const configPath = path.join(tmpDir, 'config.yaml');
  fs.writeFileSync(
    configPath,
    configYaml({ port, metricsPort, dashboardPort, webhookUrl }),
    { mode: 0o600 },
  );

  const logPath = path.join(tmpDir, 'server.log');
  const logStream = fs.createWriteStream(logPath, { flags: 'a' });
  let logs = '';

  const child = spawn(binary, ['-config', configPath], {
    cwd: ROOT,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  const capture = (chunk) => {
    const text = chunk.toString();
    logs += text;
    logStream.write(chunk);
  };

  child.stdout.on('data', capture);
  child.stderr.on('data', capture);
  child.once('exit', (code, signal) => {
    const line = `\n[server exited code=${code} signal=${signal}]\n`;
    logs += line;
    logStream.write(line);
  });

  try {
    await waitForPort(port);
  } catch (err) {
    child.kill('SIGKILL');
    await once(child, 'exit').catch(() => {});
    logStream.end();
    throw new Error(`${err.message}\n--- server log ---\n${logs}`);
  }

  const options = {
    appId: 'compat-app',
    key: 'compat-key',
    secret: 'compat-secret',
    port,
  };

  const backends = {
    compat: createBackend(options),
    origin: createBackend({
      ...options,
      appId: 'origin-app',
      key: 'origin-key',
      secret: 'origin-secret',
    }),
    other: createBackend({
      ...options,
      appId: 'other-app',
      key: 'other-key',
      secret: 'other-secret',
    }),
    official: createBackend({
      ...options,
      appId: '3',
      key: '278d425bdf160c739803',
      secret: '7ad3773142a6692b25b8',
    }),
  };

  return {
    tmpDir,
    configPath,
    logPath,
    binary,
    port,
    metricsPort,
    dashboardPort,
    baseUrl: `http://127.0.0.1:${port}`,
    backends,
    logs: () => logs,
    async stop() {
      if (child.exitCode === null) {
        child.kill('SIGTERM');
        await Promise.race([once(child, 'exit'), delay(3000)]);
        if (child.exitCode === null) {
          child.kill('SIGKILL');
          await Promise.race([once(child, 'exit'), delay(1000)]);
        }
      }

      await new Promise((resolve) => logStream.end(resolve));

      if (process.env.COMPAT_CLEANUP === '1') {
        fs.rmSync(tmpDir, { recursive: true, force: true });
      }
    },
  };
}

module.exports = {
  delay,
  getFreePort,
  startServer,
  waitForPort,
};
