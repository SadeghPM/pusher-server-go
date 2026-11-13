'use strict';

const crypto = require('node:crypto');

const Pusher = require('pusher');

function createBackend({
  appId = 'compat-app',
  key = 'compat-key',
  secret = 'compat-secret',
  port,
}) {
  return new Pusher({
    appId,
    key,
    secret,
    host: '127.0.0.1',
    port,
    useTLS: false,
  });
}

function hmac(secret, data) {
  return crypto.createHmac('sha256', secret).update(data).digest('hex');
}

function md5(data) {
  return crypto.createHash('md5').update(data).digest('hex');
}

function signChannelAuth({
  socketId,
  channel,
  channelData,
  key = 'compat-key',
  secret = 'compat-secret',
}) {
  const toSign = channel.startsWith('presence-')
    ? `${socketId}:${channel}:${channelData ?? ''}`
    : `${socketId}:${channel}`;

  return `${key}:${hmac(secret, toSign)}`;
}

/**
 * Build a request using the signing algorithm documented by Pusher:
 * METHOD\nPATH\nlowercased-and-sorted-query-params
 *
 * Values are not URL-escaped when calculating the signature; only the final
 * URL is encoded for transport.
 */
function buildSignedRequest({
  baseUrl,
  appId = 'compat-app',
  key = 'compat-key',
  secret = 'compat-secret',
  method = 'POST',
  body = '',
  timestamp = Math.floor(Date.now() / 1000),
  bodyMd5,
  extraParams = {},
}) {
  const requestPath = `/apps/${appId}/events`;
  const params = {
    auth_key: key,
    auth_timestamp: String(timestamp),
    auth_version: '1.0',
    ...(body.length > 0 ? { body_md5: bodyMd5 || md5(body) } : {}),
    ...extraParams,
  };

  const canonical = Object.keys(params)
    .map((name) => [name.toLowerCase(), params[name]])
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([name, value]) => `${name}=${value}`)
    .join('&');

  const signature = hmac(secret, `${method}\n${requestPath}\n${canonical}`);
  const query = new URLSearchParams({
    ...params,
    auth_signature: signature,
  }).toString();

  return {
    url: `${baseUrl}${requestPath}?${query}`,
    body,
    signature,
    canonical,
  };
}

module.exports = {
  buildSignedRequest,
  createBackend,
  hmac,
  md5,
  signChannelAuth,
};
