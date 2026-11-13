# Pusher Protocol v7 compatibility tests

This directory contains black-box compatibility tests for the Go server. They
use the official Pusher SDKs as the clients:

- [`pusher-js`](https://github.com/pusher/pusher-js) — the official JavaScript
  WebSocket client.
- [`pusher`](https://github.com/pusher/pusher-http-node) — the official Node.js
  server SDK for signing and triggering REST events.

No official server-side conformance suite exists. These tests are the practical
alternative: drive a real Go binary with the real client and server SDKs, and
assert the wire behavior described by the
[Pusher Protocol v7 specification](https://pusher.com/docs/channels/library_auth_reference/pusher-websockets-protocol/).

## Requirements

- Go 1.24+
- Node.js 20+

## Run

```bash
cd compat
npm install
npm test
```

The test runner builds the server into a temporary directory, writes a
temporary config, starts the binary on random free ports, and shuts it down
after the suite finishes. The server log path is printed at the end.

Useful environment variables:

- `COMPAT_CLEANUP=1` — delete the temporary directory after the run.
  By default it is kept so the server log can be inspected.

## What is covered

- Connection establishment and socket-id format
- Unknown app rejection
- `pusher:connection_established` payload shape
- `pusher:ping` / `pusher:pong`
- Per-app `allowed_origins`
- Public channel subscribe and REST-triggered delivery
- Multiple channels in one REST trigger
- `socket_id` exclusion on REST triggers
- Unsubscribe behavior
- Multi-tenant isolation between apps
- Rejection of client events on public channels
- Private channel auth success/failure
- Client events on private channels, including sender exclusion
- Presence `count` / `hash` / `ids` / `me`
- `member_added` / `member_removed`
- Duplicate presence user IDs
- Presence `channel_data` validation
- REST auth success/failure, body MD5 mismatch, unknown app, method checks
- Official HTTP API example credentials
- Webhook `channel_occupied` / `channel_vacated` / `member_added` /
  `member_removed`, including `X-Pusher-Signature` verification
- Invalid JSON and unknown-event resilience

## Known gaps

The following are marked as TODO in the test suite because they document
behavior that is not currently implemented. They are intentionally not failing
the suite, so they can be reviewed before changing server behavior:

- `auth_timestamp` is not checked against a replay window. The official HTTP
  API specifies a 600-second window.
- REST signature verification only reconstructs the four auth query parameters.
  The official algorithm signs **all** query parameters, sorted by key.
- The 10 KB event `data` limit from the official HTTP API is not enforced.
- `pusher:error` payloads currently use `"code": null` instead of a numeric
  error code.

## Why not run the official `pusher-js` integration tests directly?

The official integration tests are designed for Pusher's hosted service:

- They hardcode the `mt1` cluster and a Pusher app key.
- They require `INTEGRATION_TESTS_APP_*` credentials.
- Their auxiliary server proxies triggers to Pusher's cloud API.
- The `pusher-js` README notes that integration tests connect to production.

The test scenarios in that suite (public channels, subscription state, client
events, presence) are still a useful reference. This suite uses the same real
client but points it at the local server with `wsHost` / `wsPort`.
