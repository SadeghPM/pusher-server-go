'use strict';

const { after, before, describe, test } = require('node:test');
const assert = require('node:assert/strict');

const { startServer } = require('../helpers/server');
const { startWebhookReceiver } = require('../helpers/webhook');
const {
  connectRaw,
  createClient,
  disconnect,
  expectNoEvent,
  rawSubscribe,
  tryConnectRaw,
  waitForConnection,
  waitForEvent,
  waitForSubscription,
} = require('../helpers/client');
const {
  buildSignedRequest,
  createBackend,
  signChannelAuth,
} = require('../helpers/rest');

let server;
let webhooks;

before(async () => {
  webhooks = await startWebhookReceiver();
  server = await startServer({ webhookUrl: webhooks.url });
});

after(async () => {
  if (server) {
    await server.stop();
  }
  if (webhooks) {
    await webhooks.close();
  }
  if (server) {
    console.log(`compat server log: ${server.logPath}`);
  }
});

let channelCounter = 0;

function channelName(prefix) {
  channelCounter += 1;
  return `${prefix}-${Date.now()}-${channelCounter}`;
}

function makeClient(t, options = {}) {
  const client = createClient({
    port: server.port,
    backend: server.backends.compat,
    ...options,
  });

  t.after(() => disconnect(client));
  return client;
}

async function makeConnectedClient(t, options = {}) {
  const client = makeClient(t, options);
  await waitForConnection(client);
  return client;
}

async function subscribeClient(client, name) {
  const channel = client.subscribe(name);
  const members = await waitForSubscription(channel);
  return { channel, members };
}

describe('connection', () => {
  test('official pusher-js client connects and gets a valid socket id', async (t) => {
    const client = await makeConnectedClient(t);

    assert.equal(client.connection.state, 'connected');
    assert.match(client.connection.socket_id, /^\d+\.\d+$/);
  });

  test('assigns a unique socket id to every connection', async (t) => {
    const first = await makeConnectedClient(t);
    const second = await makeConnectedClient(t);

    assert.notEqual(first.connection.socket_id, second.connection.socket_id);
  });

  test('rejects an unknown app key before the WebSocket upgrade', async () => {
    const result = await tryConnectRaw({
      port: server.port,
      appKey: 'missing-key',
    });

    assert.equal(result.ok, false);
    assert.equal(result.status, 404);
  });

  test('connection_established data is a JSON-encoded string with socket_id and activity_timeout', async (t) => {
    const raw = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => raw.close());

    assert.equal(typeof raw.established.data, 'string');

    const data = JSON.parse(raw.established.data);
    assert.match(data.socket_id, /^\d+\.\d+$/);
    assert.equal(typeof data.activity_timeout, 'number');
    assert.ok(data.activity_timeout > 0);
  });

  test('responds to pusher:ping with pusher:pong', async (t) => {
    const raw = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => raw.close());

    raw.send({ event: 'pusher:ping', data: {} });
    const pong = await raw.next('pusher:pong');

    assert.equal(typeof pong.data, 'string');
    assert.deepEqual(JSON.parse(pong.data), {});
  });

  test('enforces allowed_origins per app', async () => {
    const allowed = await tryConnectRaw({
      port: server.port,
      appKey: 'origin-key',
      origin: 'http://allowed.example',
    });
    assert.equal(allowed.ok, true);

    const denied = await tryConnectRaw({
      port: server.port,
      appKey: 'origin-key',
      origin: 'http://denied.example',
    });
    assert.equal(denied.ok, false);
    assert.equal(denied.status, 403);

    const noOrigin = await tryConnectRaw({
      port: server.port,
      appKey: 'origin-key',
    });
    assert.equal(noOrigin.ok, true);
  });
});

describe('public channels', () => {
  test('subscribes and receives an event triggered through the official REST SDK', async (t) => {
    const client = await makeConnectedClient(t);
    const name = channelName('public');
    const { channel } = await subscribeClient(client, name);

    const received = waitForEvent(channel, 'greeting');
    await server.backends.compat.trigger(name, 'greeting', {
      message: 'hello',
      nested: { ok: true },
    });

    assert.deepEqual(await received, {
      message: 'hello',
      nested: { ok: true },
    });
  });

  test('REST trigger supports multiple channels', async (t) => {
    const first = await makeConnectedClient(t);
    const second = await makeConnectedClient(t);
    const firstChannel = channelName('multi-first');
    const secondChannel = channelName('multi-second');

    const { channel: channelOne } = await subscribeClient(first, firstChannel);
    const { channel: channelTwo } = await subscribeClient(second, secondChannel);

    const firstEvent = waitForEvent(channelOne, 'batch');
    const secondEvent = waitForEvent(channelTwo, 'batch');

    await server.backends.compat.trigger(
      [firstChannel, secondChannel],
      'batch',
      { n: 1 },
    );

    assert.deepEqual(await firstEvent, { n: 1 });
    assert.deepEqual(await secondEvent, { n: 1 });
  });

  test('REST trigger excludes the given socket_id', async (t) => {
    const first = await makeConnectedClient(t);
    const second = await makeConnectedClient(t);
    const name = channelName('exclude');

    const { channel: firstChannel } = await subscribeClient(first, name);
    const { channel: secondChannel } = await subscribeClient(second, name);

    const secondEvent = waitForEvent(secondChannel, 'only-second');
    const firstMustNotReceive = expectNoEvent(firstChannel, 'only-second', 500);

    await server.backends.compat.trigger(
      name,
      'only-second',
      { ok: true },
      { socket_id: first.connection.socket_id },
    );

    assert.deepEqual(await secondEvent, { ok: true });
    await firstMustNotReceive;
  });

  test('stops delivering events after unsubscribe', async (t) => {
    const client = await makeConnectedClient(t);
    const name = channelName('unsubscribe');
    const { channel } = await subscribeClient(client, name);

    client.unsubscribe(name);
    await new Promise((resolve) => setTimeout(resolve, 100));

    const noEvent = expectNoEvent(channel, 'after-unsubscribe', 500);
    await server.backends.compat.trigger(name, 'after-unsubscribe', 'nope');

    await noEvent;
  });

  test('isolates channels between apps', async (t) => {
    const compat = await makeConnectedClient(t);
    const other = await makeConnectedClient(t, {
      backend: server.backends.other,
      key: 'other-key',
    });

    const name = channelName('isolated');
    const { channel: compatChannel } = await subscribeClient(compat, name);
    const { channel: otherChannel } = await subscribeClient(other, name);

    const otherEvent = waitForEvent(otherChannel, 'leak');
    const compatMustNotReceive = expectNoEvent(compatChannel, 'leak', 500);

    await server.backends.other.trigger(name, 'leak', 'only-other-app');

    assert.equal(await otherEvent, 'only-other-app');
    await compatMustNotReceive;
  });

  test('ignores client events on public channels', async (t) => {
    const first = await connectRaw({ port: server.port, appKey: 'compat-key' });
    const second = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => first.close());
    t.after(() => second.close());

    const name = channelName('public-client-event');
    first.send({ event: 'pusher:subscribe', data: { channel: name } });
    second.send({ event: 'pusher:subscribe', data: { channel: name } });

    await first.next('pusher_internal:subscription_succeeded');
    await second.next('pusher_internal:subscription_succeeded');

    const noEvent = second.expectNoEvent('client-nope', 500);
    first.send({
      event: 'client-nope',
      channel: name,
      data: { text: 'should be ignored' },
    });

    await noEvent;
  });
});

describe('private channels', () => {
  test('subscribes with a valid auth signature', async (t) => {
    const client = await makeConnectedClient(t);
    const name = channelName('private');
    const { channel } = await subscribeClient(client, name);

    assert.equal(channel.subscribed, true);
  });

  test('rejects an invalid auth signature with a PusherError', async (t) => {
    const client = createClient({
      port: server.port,
      backend: server.backends.compat,
      authorizer: (_channel, _socketId, callback) => {
        callback(false, { auth: 'compat-key:deadbeef' });
      },
    });
    t.after(() => disconnect(client));

    const errorPromise = waitForEvent(client.connection, 'error');
    await waitForConnection(client);

    const channel = client.subscribe(channelName('private-invalid'));
    const error = await errorPromise;

    assert.equal(error.type, 'PusherError');
    assert.match(error.data.message, /Invalid signature/i);
    assert.equal(channel.subscribed, false);
  });

  test('delivers client events to other subscribers but not back to the sender', async (t) => {
    const first = await makeConnectedClient(t);
    const second = await makeConnectedClient(t);
    const name = channelName('private-client-event');

    const { channel: sender } = await subscribeClient(first, name);
    const { channel: receiver } = await subscribeClient(second, name);

    const received = waitForEvent(receiver, 'client-hello');
    const senderMustNotReceive = expectNoEvent(sender, 'client-hello', 500);

    sender.trigger('client-hello', { text: 'hi' });

    assert.deepEqual(await received, { text: 'hi' });
    await senderMustNotReceive;
  });

  test('ignores client events on a private channel the sender did not join', async (t) => {
    const subscribed = await connectRaw({ port: server.port, appKey: 'compat-key' });
    const outsider = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => subscribed.close());
    t.after(() => outsider.close());

    const name = channelName('private-unjoined');
    rawSubscribe(subscribed, name);
    await subscribed.next('pusher_internal:subscription_succeeded');

    const noEvent = subscribed.expectNoEvent('client-nope', 500);
    outsider.send({
      event: 'client-nope',
      channel: name,
      data: { text: 'should be ignored' },
    });

    await noEvent;
  });
});

describe('presence channels', () => {
  test('subscribes and exposes count, hash, ids, and me', async (t) => {
    const client = await makeConnectedClient(t, {
      user: { id: 'alice', info: { name: 'Alice' } },
    });

    const name = channelName('presence');
    const { members } = await subscribeClient(client, name);

    assert.equal(members.count, 1);
    assert.equal(members.me.id, 'alice');
    assert.deepEqual(members.me.info, { name: 'Alice' });
    assert.equal(members.get('alice').info.name, 'Alice');
  });

  test('emits member_added and member_removed', async (t) => {
    const alice = await makeConnectedClient(t, {
      user: { id: 'alice', info: { name: 'Alice' } },
    });
    const bob = await makeConnectedClient(t, {
      user: { id: 'bob', info: { name: 'Bob' } },
    });

    const name = channelName('presence-members');
    const { channel: aliceChannel } = await subscribeClient(alice, name);

    const added = waitForEvent(aliceChannel, 'pusher:member_added');
    await subscribeClient(bob, name);

    const member = await added;
    assert.equal(member.id, 'bob');
    assert.deepEqual(member.info, { name: 'Bob' });
    assert.equal(aliceChannel.members.count, 2);

    const removed = waitForEvent(aliceChannel, 'pusher:member_removed');
    await disconnect(bob);

    const gone = await removed;
    assert.equal(gone.id, 'bob');
    assert.equal(aliceChannel.members.count, 1);
  });

  test('does not duplicate members for the same user_id', async (t) => {
    const observer = await makeConnectedClient(t, {
      user: { id: 'observer', info: { name: 'Observer' } },
    });
    const first = await makeConnectedClient(t, {
      user: { id: 'same', info: { name: 'First' } },
    });
    const second = await makeConnectedClient(t, {
      user: { id: 'same', info: { name: 'Second' } },
    });

    const name = channelName('presence-duplicate');
    const { channel: observerChannel } = await subscribeClient(observer, name);

    const added = waitForEvent(observerChannel, 'pusher:member_added');
    await subscribeClient(first, name);
    assert.equal((await added).id, 'same');

    const noDuplicateAdded = expectNoEvent(
      observerChannel,
      'pusher:member_added',
      600,
    );
    const { members: secondMembers } = await subscribeClient(second, name);
    assert.equal(secondMembers.count, 2);
    await noDuplicateAdded;

    const noEarlyRemoval = expectNoEvent(
      observerChannel,
      'pusher:member_removed',
      600,
    );
    await disconnect(second);
    await noEarlyRemoval;

    const removed = waitForEvent(observerChannel, 'pusher:member_removed');
    await disconnect(first);

    assert.equal((await removed).id, 'same');
  });

  test('presence subscription_succeeded uses the documented count/hash/ids shape', async (t) => {
    const raw = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => raw.close());

    const name = channelName('presence-shape');
    const channelData = JSON.stringify({
      user_id: 'raw-user',
      user_info: { name: 'Raw' },
    });

    raw.send({
      event: 'pusher:subscribe',
      data: {
        channel: name,
        channel_data: channelData,
        auth: signChannelAuth({
          socketId: raw.socketId,
          channel: name,
          channelData,
        }),
      },
    });

    const success = await raw.next('pusher_internal:subscription_succeeded');
    const payload = JSON.parse(success.data);

    assert.equal(payload.presence.count, 1);
    assert.deepEqual(payload.presence.ids, ['raw-user']);
    assert.deepEqual(payload.presence.hash, {
      'raw-user': { name: 'Raw' },
    });
  });

  test('rejects presence subscriptions without channel_data', async (t) => {
    const raw = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => raw.close());

    const name = channelName('presence-missing-data');
    raw.send({
      event: 'pusher:subscribe',
      data: {
        channel: name,
        auth: signChannelAuth({
          socketId: raw.socketId,
          channel: name,
        }),
      },
    });

    const error = await raw.next('pusher:error');
    const payload = JSON.parse(error.data);

    assert.match(payload.message, /channel_data/i);
  });

  test('rejects malformed presence channel_data', async (t) => {
    const raw = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => raw.close());

    const name = channelName('presence-malformed-data');
    const channelData = 'not-json';

    raw.send({
      event: 'pusher:subscribe',
      data: {
        channel: name,
        channel_data: channelData,
        auth: signChannelAuth({
          socketId: raw.socketId,
          channel: name,
          channelData,
        }),
      },
    });

    const error = await raw.next('pusher:error');
    const payload = JSON.parse(error.data);

    assert.match(payload.message, /Invalid channel_data/i);
  });
});

describe('REST API', () => {
  test('official SDK trigger returns 200 and an empty JSON object', async () => {
    const response = await server.backends.compat.trigger(
      channelName('rest-sdk'),
      'event',
      { ok: true },
    );

    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), {});
  });

  test('rejects an invalid app secret with 401', async () => {
    const backend = createBackend({
      appId: 'compat-app',
      key: 'compat-key',
      secret: 'wrong-secret',
      port: server.port,
    });

    await assert.rejects(
      backend.trigger(channelName('rest-bad-secret'), 'event', 'x'),
      (error) => {
        assert.equal(error.status, 401);
        assert.match(error.body, /Invalid signature/i);
        return true;
      },
    );
  });

  test('rejects a body MD5 mismatch', async () => {
    const body = JSON.stringify({
      name: 'event',
      channel: channelName('rest-md5'),
      data: 'x',
    });
    const signed = buildSignedRequest({
      baseUrl: server.baseUrl,
      appId: 'compat-app',
      key: 'compat-key',
      secret: 'compat-secret',
      body,
      bodyMd5: '0'.repeat(32),
    });

    const response = await fetch(signed.url, { method: 'POST', body });

    assert.equal(response.status, 401);
    assert.match(await response.text(), /MD5/i);
  });

  test('accepts a valid request signed with the documented algorithm', async () => {
    const channel = channelName('rest-manual');
    const body = JSON.stringify({
      name: 'manual-event',
      channel,
      data: '{"ok":true}',
    });
    const signed = buildSignedRequest({
      baseUrl: server.baseUrl,
      appId: 'compat-app',
      key: 'compat-key',
      secret: 'compat-secret',
      body,
    });

    const response = await fetch(signed.url, { method: 'POST', body });

    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), {});
  });

  test('accepts the official HTTP API example credentials', async () => {
    const channel = channelName('official-credentials');
    const body = JSON.stringify({
      name: 'foo',
      channels: [channel],
      data: '{"some":"data"}',
    });
    const signed = buildSignedRequest({
      baseUrl: server.baseUrl,
      appId: '3',
      key: '278d425bdf160c739803',
      secret: '7ad3773142a6692b25b8',
      body,
    });

    const response = await fetch(signed.url, { method: 'POST', body });

    assert.equal(response.status, 200);
  });

  test('rejects an unknown app id with 404', async () => {
    const response = await fetch(`${server.baseUrl}/apps/missing-app/events`, {
      method: 'POST',
      body: '{}',
    });

    assert.equal(response.status, 404);
  });

  test('rejects non-POST methods with 405', async () => {
    const response = await fetch(`${server.baseUrl}/apps/compat-app/events`);

    assert.equal(response.status, 405);
  });

  // These are intentionally pending until the corresponding server behavior
  // is implemented or explicitly accepted. See compat/README.md.
  test.todo('rejects auth_timestamp older than 600 seconds');
  test.todo('signs additional query parameters as required by the official algorithm');
  test.todo('enforces the 10 KB event data limit');
});

describe('webhooks', () => {
  test('sends channel_occupied and channel_vacated with a valid signature', async (t) => {
    const client = await makeConnectedClient(t);
    const name = channelName('webhook-public');
    const { channel } = await subscribeClient(client, name);

    const occupied = await webhooks.waitFor(
      (event) => event.name === 'channel_occupied' && event.channel === name,
    );

    assert.equal(occupied.record.headers['x-pusher-key'], 'compat-key');
    assert.equal(webhooks.verifySignature(occupied.record, 'compat-secret'), true);

    client.unsubscribe(name);

    const vacated = await webhooks.waitFor(
      (event) => event.name === 'channel_vacated' && event.channel === name,
    );

    assert.equal(webhooks.verifySignature(vacated.record, 'compat-secret'), true);
    assert.equal(channel.subscribed, false);
  });

  test('sends member_added and member_removed webhooks', async (t) => {
    const alice = await makeConnectedClient(t, {
      user: { id: 'hook-alice', info: { name: 'Alice' } },
    });
    const bob = await makeConnectedClient(t, {
      user: { id: 'hook-bob', info: { name: 'Bob' } },
    });

    const name = channelName('presence-webhook');
    const { channel: aliceChannel } = await subscribeClient(alice, name);

    const addedEvent = waitForEvent(aliceChannel, 'pusher:member_added');
    await subscribeClient(bob, name);
    await addedEvent;

    const added = await webhooks.waitFor(
      (event) =>
        event.name === 'member_added' &&
        event.channel === name &&
        event.user_id === 'hook-bob',
    );
    assert.ok(added);

    await disconnect(bob);

    const removed = await webhooks.waitFor(
      (event) =>
        event.name === 'member_removed' &&
        event.channel === name &&
        event.user_id === 'hook-bob',
    );
    assert.ok(removed);
  });
});

describe('protocol edge cases', () => {
  test('keeps the connection alive after invalid JSON', async (t) => {
    const raw = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => raw.close());

    raw.sendRaw('this is not json');
    raw.send({ event: 'pusher:ping', data: {} });

    const pong = await raw.next('pusher:pong');
    assert.ok(pong);
  });

  test('sends pusher:error for an invalid private channel signature', async (t) => {
    const raw = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => raw.close());

    raw.send({
      event: 'pusher:subscribe',
      data: {
        channel: channelName('private-raw-invalid'),
        auth: 'compat-key:deadbeef',
      },
    });

    const error = await raw.next('pusher:error');
    const payload = JSON.parse(error.data);

    assert.match(payload.message, /Invalid signature/i);
  });

  test('ignores unknown events and keeps the connection usable', async (t) => {
    const raw = await connectRaw({ port: server.port, appKey: 'compat-key' });
    t.after(() => raw.close());

    raw.send({ event: 'totally-unknown', data: { x: 1 } });
    raw.send({ event: 'pusher:ping', data: {} });

    const pong = await raw.next('pusher:pong');
    assert.ok(pong);
  });
});
