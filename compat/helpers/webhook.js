'use strict';

const { EventEmitter } = require('node:events');
const crypto = require('node:crypto');
const http = require('node:http');

async function startWebhookReceiver(port = 0) {
  const emitter = new EventEmitter();
  const requests = [];

  const server = http.createServer((request, response) => {
    const chunks = [];
    request.on('data', (chunk) => chunks.push(chunk));
    request.on('end', () => {
      const rawBody = Buffer.concat(chunks).toString('utf8');
      let body;

      try {
        body = JSON.parse(rawBody);
      } catch (error) {
        requests.push({
          headers: request.headers,
          rawBody,
          parseError: error.message,
        });
        response.writeHead(400, { 'Content-Type': 'text/plain' });
        response.end('invalid JSON');
        return;
      }

      const record = { headers: request.headers, rawBody, body };
      requests.push(record);

      for (const event of body.events || []) {
        emitter.emit('event', { ...event, time_ms: body.time_ms, record });
      }

      response.writeHead(200, { 'Content-Type': 'text/plain' });
      response.end('ok');
    });
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', resolve);
  });

  const actualPort = server.address().port;

  function find(predicate) {
    for (const record of requests) {
      for (const event of (record.body && record.body.events) || []) {
        if (predicate(event, record)) {
          return { event, record };
        }
      }
    }
    return null;
  }

  return {
    port: actualPort,
    url: `http://127.0.0.1:${actualPort}/webhook`,
    requests,

    waitFor(predicate, timeout = 5000) {
      const existing = find(predicate);
      if (existing) {
        return Promise.resolve(existing);
      }

      return new Promise((resolve, reject) => {
        let timer;

        const cleanup = () => {
          clearTimeout(timer);
          emitter.off('event', handler);
        };

        const handler = (event) => {
          if (predicate(event, event.record)) {
            cleanup();
            resolve({ event, record: event.record });
          }
        };

        timer = setTimeout(() => {
          cleanup();
          reject(new Error('Timed out waiting for a webhook'));
        }, timeout);

        emitter.on('event', handler);
      });
    },

    verifySignature(record, secret) {
      const expected = crypto
        .createHmac('sha256', secret)
        .update(record.rawBody)
        .digest('hex');
      const actual = record.headers['x-pusher-signature'];

      if (typeof actual !== 'string' || actual.length !== expected.length) {
        return false;
      }

      return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(actual));
    },

    close() {
      return new Promise((resolve) => server.close(resolve));
    },
  };
}

module.exports = { startWebhookReceiver };
