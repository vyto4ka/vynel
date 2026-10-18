-- The addresses nodes connect to the gateway from: the firewall lets only them reach the
-- gateway port (docs/INSTALL_GUIDE.md §10).

-- +goose Up
ALTER TABLE nodes ADD COLUMN peer_ips TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE nodes DROP COLUMN peer_ips;
