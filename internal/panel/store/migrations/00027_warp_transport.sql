-- Add WARP tunnel transport ('masque-h3', 'masque-h2', 'wireguard').
-- Existing rows default to 'wireguard' to match their stored X25519 keys until re-provisioned or auto-upgraded.

-- +goose Up
ALTER TABLE node_warp ADD COLUMN transport TEXT NOT NULL DEFAULT 'wireguard';

-- +goose Down
ALTER TABLE node_warp DROP COLUMN transport;
