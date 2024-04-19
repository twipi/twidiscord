PRAGMA strict = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE accounts (
	user_number TEXT PRIMARY KEY,
	server_number TEXT NOT NULL,
	discord_token TEXT NOT NULL UNIQUE
);

CREATE TABLE numbers_muted (
	user_number TEXT PRIMARY KEY REFERENCES accounts(user_number),
	muted INT NOT NULL DEFAULT 0,
	until INT NOT NULL DEFAULT 0
);

CREATE TABLE channel_nicknames (
	user_number TEXT NOT NULL REFERENCES accounts(user_number),
	channel_id BIGINT NOT NULL,
	nickname TEXT NOT NULL,
	UNIQUE(user_number, channel_id)
);
