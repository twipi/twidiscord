-- name: SetAccount :exec
REPLACE INTO accounts (user_number, server_number, discord_token) VALUES (?, ?, ?);

-- name: Account :one
SELECT server_number, discord_token FROM accounts WHERE user_number = ? LIMIT 1;

-- name: Accounts :many
SELECT user_number, server_number, discord_token FROM accounts;

-- name: NumberIsMuted :one
SELECT muted FROM numbers_muted
	WHERE user_number = ? AND (until = 0 OR until > NOW())
	LIMIT 1;

-- name: SetNumberMuted :exec
REPLACE INTO numbers_muted (user_number, muted, until) VALUES (?, ?, ?);

-- name: ChannelNickname :one
SELECT nickname FROM channel_nicknames WHERE user_number = ? AND channel_id = ? LIMIT 1;

-- name: ChannelNicknames :many
SELECT channel_id, nickname FROM channel_nicknames WHERE user_number = ?;

-- name: ChannelFromNickname :one
SELECT channel_id FROM channel_nicknames WHERE user_number = ? AND nickname = ? LIMIT 1;

-- name: SetChannelNickname :exec
REPLACE INTO channel_nicknames (user_number, channel_id, nickname) VALUES (?, ?, ?);
