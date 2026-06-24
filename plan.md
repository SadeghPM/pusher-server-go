1. **Update `core/hub.go` and models**:
   - Change `Channels` map to store members instead of booleans: `map[string]map[*Client]*ChannelMember`.
   - Add `ChannelMember` struct to hold `UserID` and `UserInfo` for presence channels.
   - Implement `Unsubscribe` method.
   - Modify `UnregisterClient` and `Unsubscribe` to detect when a unique `UserID` leaves a presence channel, and send `pusher_internal:member_removed`.
   - Modify `Subscribe` to detect when a new unique `UserID` joins a presence channel, and return a flag so we can send `pusher_internal:member_added`.

2. **Update `server/websocket.go` structs**:
   - Add `Channel` to `PusherEvent`.
   - Add `ChannelData` to `PusherSubscribeData`.
   - Create `PusherUnsubscribeData`.
   - Create `ChannelData` struct to parse presence channel subscription info.

3. **Implement `pusher:subscribe` presence logic**:
   - For `presence-` channels, verify signature `socket_id:channel_name:channel_data`.
   - Parse `channel_data` to extract `user_id` and `user_info`.
   - Send `pusher_internal:subscription_succeeded` with the required `presence` hash.
   - Broadcast `pusher_internal:member_added` to others if a new `user_id` joined.

4. **Implement `pusher:unsubscribe`**:
   - Handle `pusher:unsubscribe` by calling `AppHub.Unsubscribe`.

5. **Implement `client-*` events**:
   - Intercept events prefixed with `client-`.
   - Verify channel is `private-` or `presence-` and client is subscribed.
   - Broadcast the event to the channel.
   - Append `user_id` to the payload if it's a `presence-` channel.

6. **Pre-commit and Tests**:
   - Call `pre_commit_instructions` to ensure proper testing, verification, review, and reflection are done.
   - Verify changes with `go test ./...`.
