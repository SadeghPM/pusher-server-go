'use strict';

const Pusher = require('pusher-js');
const WebSocket = require('ws');

const { signChannelAuth } = require('./rest');

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function createClient({
  port,
  backend,
  key = 'compat-key',
  user,
  authorizer,
  overrides = {},
}) {
  const options = {
    wsHost: '127.0.0.1',
    wsPort: port,
    wssPort: port,
    httpHost: '127.0.0.1',
    httpPort: port,
    httpsPort: port,
    forceTLS: false,
    enabledTransports: ['ws'],
    disableStats: true,
    cluster: 'mt1',
    ...overrides,
  };

  if (!options.authorizer) {
    options.authorizer = (channel) => ({
      authorize: (socketId, callback) => {
        if (authorizer) {
          authorizer(channel, socketId, callback);
          return;
        }

        try {
          const channelData =
            channel.name.startsWith('presence-') && user
              ? { user_id: user.id, user_info: user.info }
              : undefined;

          callback(false, backend.authorizeChannel(socketId, channel.name, channelData));
        } catch (err) {
          callback(true, err);
        }
      },
    });
  }

  return new Pusher(key, options);
}

function waitForConnection(client, timeout = 5000) {
  return new Promise((resolve, reject) => {
    if (client.connection.state === 'connected') {
      resolve();
      return;
    }

    let timer;

    const cleanup = () => {
      clearTimeout(timer);
      client.connection.unbind('state_change', onState);
      client.connection.unbind('error', onError);
    };

    const onState = ({ current }) => {
      if (current === 'connected') {
        cleanup();
        resolve();
      }
    };

    const onError = (error) => {
      cleanup();
      reject(
        error instanceof Error
          ? error
          : new Error(`Connection error: ${JSON.stringify(error)}`),
      );
    };

    timer = setTimeout(() => {
      cleanup();
      reject(new Error('Timed out waiting for the Pusher connection'));
    }, timeout);

    client.connection.bind('state_change', onState);
    client.connection.bind('error', onError);
  });
}

function waitForSubscription(channel, timeout = 5000) {
  return new Promise((resolve, reject) => {
    if (channel.subscribed) {
      resolve(channel.members);
      return;
    }

    let timer;

    const cleanup = () => {
      clearTimeout(timer);
      channel.unbind('pusher:subscription_succeeded', onSuccess);
      channel.unbind('pusher:subscription_error', onError);
    };

    const onSuccess = (members) => {
      cleanup();
      resolve(members);
    };

    const onError = (error) => {
      cleanup();
      reject(
        error instanceof Error
          ? error
          : new Error(`Subscription error: ${JSON.stringify(error)}`),
      );
    };

    timer = setTimeout(() => {
      cleanup();
      reject(new Error(`Timed out waiting for subscription to ${channel.name}`));
    }, timeout);

    channel.bind('pusher:subscription_succeeded', onSuccess);
    channel.bind('pusher:subscription_error', onError);
  });
}

function waitForEvent(emitter, event, { timeout = 3000, predicate } = {}) {
  return new Promise((resolve, reject) => {
    let timer;

    const cleanup = () => {
      clearTimeout(timer);
      emitter.unbind(event, handler);
    };

    const handler = (data) => {
      if (predicate && !predicate(data)) {
        return;
      }
      cleanup();
      resolve(data);
    };

    timer = setTimeout(() => {
      cleanup();
      reject(new Error(`Timed out waiting for ${event}`));
    }, timeout);

    emitter.bind(event, handler);
  });
}

function expectNoEvent(emitter, event, timeout = 500) {
  return new Promise((resolve, reject) => {
    let timer;

    const cleanup = () => {
      clearTimeout(timer);
      emitter.unbind(event, handler);
    };

    const handler = (data) => {
      cleanup();
      reject(new Error(`Unexpected event ${event}: ${JSON.stringify(data)}`));
    };

    timer = setTimeout(() => {
      cleanup();
      resolve();
    }, timeout);

    emitter.bind(event, handler);
  });
}

function disconnect(client) {
  return new Promise((resolve) => {
    if (!client || client.connection.state === 'disconnected') {
      resolve();
      return;
    }

    let timer;

    const cleanup = () => {
      clearTimeout(timer);
      client.connection.unbind('state_change', onState);
    };

    const onState = ({ current }) => {
      if (current === 'disconnected') {
        cleanup();
        resolve();
      }
    };

    timer = setTimeout(() => {
      cleanup();
      resolve();
    }, 2000);

    client.connection.bind('state_change', onState);

    try {
      client.disconnect();
    } catch {
      cleanup();
      resolve();
    }
  });
}

function waitForOpen(ws) {
  return new Promise((resolve, reject) => {
    let settled = false;

    const cleanup = () => {
      ws.off('open', onOpen);
      ws.off('error', onError);
      ws.off('unexpected-response', onUnexpectedResponse);
    };

    const finish = (callback, value) => {
      if (settled) {
        return;
      }
      settled = true;
      cleanup();
      callback(value);
    };

    const onOpen = () => finish(resolve);
    const onError = (error) => finish(reject, error);
    const onUnexpectedResponse = (_request, response) =>
      finish(
        reject,
        Object.assign(new Error(`WebSocket upgrade failed with HTTP ${response.statusCode}`), {
          statusCode: response.statusCode,
        }),
      );

    ws.on('open', onOpen);
    ws.on('error', onError);
    ws.on('unexpected-response', onUnexpectedResponse);
  });
}

class RawClient {
  constructor(ws) {
    this.ws = ws;
    this.messages = [];
    this.waiters = [];
    this.closed = false;
    this.closeInfo = null;
    this.established = null;
    this.socketId = null;

    ws.on('message', (data) => {
      const text = data.toString();
      let message;
      try {
        message = JSON.parse(text);
      } catch {
        message = { raw: text };
      }
      this.messages.push(message);
      this.flush();
    });

    ws.on('close', (code, reason) => {
      this.closed = true;
      this.closeInfo = { code, reason: reason ? reason.toString() : '' };
      const waiters = this.waiters;
      this.waiters = [];
      for (const waiter of waiters) {
        clearTimeout(waiter.timer);
        waiter.reject(new Error(`Connection closed while waiting for ${waiter.label}`));
      }
    });

    // Always attach an error listener so ws does not emit an unhandled error.
    ws.on('error', () => {});
  }

  flush() {
    for (let i = this.waiters.length - 1; i >= 0; i--) {
      const waiter = this.waiters[i];
      const index = this.messages.findIndex(waiter.predicate);
      if (index === -1) {
        continue;
      }

      const [message] = this.messages.splice(index, 1);
      clearTimeout(waiter.timer);
      this.waiters.splice(i, 1);
      waiter.onMatch(message);
    }
  }

  next(predicate, timeout = 3000) {
    const fn = typeof predicate === 'string' ? (message) => message.event === predicate : predicate;
    const label = typeof predicate === 'string' ? predicate : 'matching message';
    const index = this.messages.findIndex(fn);

    if (index !== -1) {
      return Promise.resolve(this.messages.splice(index, 1)[0]);
    }

    if (this.closed) {
      return Promise.reject(new Error(`Connection already closed while waiting for ${label}`));
    }

    return new Promise((resolve, reject) => {
      const waiter = {
        predicate: fn,
        label,
        timer: null,
        onMatch: resolve,
        reject,
      };

      waiter.timer = setTimeout(() => {
        this.waiters = this.waiters.filter((item) => item !== waiter);
        reject(new Error(`Timed out waiting for ${label}`));
      }, timeout);

      this.waiters.push(waiter);
    });
  }

  expectNoEvent(event, timeout = 500) {
    const predicate = (message) => message.event === event;
    const existing = this.messages.find(predicate);

    if (existing) {
      return Promise.reject(
        new Error(`Unexpected event ${event}: ${JSON.stringify(existing)}`),
      );
    }

    if (this.closed) {
      return Promise.resolve();
    }

    return new Promise((resolve, reject) => {
      const waiter = {
        predicate,
        label: `no ${event}`,
        timer: null,
        onMatch: (message) => {
          reject(new Error(`Unexpected event ${event}: ${JSON.stringify(message)}`));
        },
        reject,
      };

      waiter.timer = setTimeout(() => {
        this.waiters = this.waiters.filter((item) => item !== waiter);
        resolve();
      }, timeout);

      this.waiters.push(waiter);
    });
  }

  send(message) {
    this.ws.send(JSON.stringify(message));
  }

  sendRaw(message) {
    this.ws.send(message);
  }

  close() {
    try {
      this.ws.close();
    } catch {
      // Already closed.
    }
  }
}

async function connectRaw({ port, appKey = 'compat-key', origin }) {
  const url = `ws://127.0.0.1:${port}/app/${appKey}?protocol=7&client=compat&version=1.0.0&flash=false`;
  const ws = new WebSocket(url, origin ? { headers: { Origin: origin } } : {});
  const raw = new RawClient(ws);

  await waitForOpen(ws);

  raw.established = await raw.next('pusher:connection_established');
  raw.socketId = JSON.parse(raw.established.data).socket_id;

  return raw;
}

function tryConnectRaw({ port, appKey, origin }) {
  const url = `ws://127.0.0.1:${port}/app/${appKey}?protocol=7&client=compat&version=1.0.0&flash=false`;

  return new Promise((resolve) => {
    const ws = new WebSocket(url, origin ? { headers: { Origin: origin } } : {});
    let settled = false;

    const finish = (result) => {
      if (settled) {
        return;
      }
      settled = true;
      try {
        ws.close();
      } catch {
        // Already closed.
      }
      resolve(result);
    };

    ws.once('open', () => finish({ ok: true }));
    ws.once('unexpected-response', (_request, response) =>
      finish({ ok: false, status: response.statusCode }),
    );
    ws.once('error', (error) => finish({ ok: false, error }));
  });
}

function rawSubscribe(raw, channel, { channelData } = {}) {
  const data = { channel };

  if (channelData) {
    data.channel_data = channelData;
    data.auth = signChannelAuth({
      socketId: raw.socketId,
      channel,
      channelData,
    });
  } else if (channel.startsWith('private-')) {
    data.auth = signChannelAuth({
      socketId: raw.socketId,
      channel,
    });
  }

  raw.send({ event: 'pusher:subscribe', data });
}

module.exports = {
  RawClient,
  connectRaw,
  createClient,
  delay,
  disconnect,
  expectNoEvent,
  rawSubscribe,
  tryConnectRaw,
  waitForConnection,
  waitForEvent,
  waitForSubscription,
};
