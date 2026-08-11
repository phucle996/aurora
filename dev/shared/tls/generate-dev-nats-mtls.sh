#!/usr/bin/env bash
set -euo pipefail

# Development-only PKI. The generated key/certificate files are ignored by the
# repository; this script is the reproducible source for the local fixtures.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
output_dir="$root_dir/dev/shared/tls/nats"
zone_mount_dir="$root_dir/dev/zone/nats/tls"
dataplane_mount_dir="$root_dir/dataplane/.secrets/nats-zone"
mkdir -p "$output_dir"
umask 077

ca_key="$output_dir/ca.key"
ca_cert="$output_dir/ca.crt"
ca_serial="$output_dir/ca.srl"

if [[ ! -s "$ca_key" || ! -s "$ca_cert" ]]; then
  openssl genrsa -out "$ca_key" 3072 >/dev/null 2>&1
  openssl req -x509 -new -sha256 -days 3650 \
    -key "$ca_key" \
    -out "$ca_cert" \
    -subj "/CN=Aurora Development Shared CA" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:1" \
    -addext "keyUsage=critical,keyCertSign,cRLSign"
fi

if [[ ! -s "$output_dir/server.key" || ! -s "$output_dir/server.crt" ]]; then
  openssl genrsa -out "$output_dir/server.key" 2048 >/dev/null 2>&1
  openssl req -new -sha256 -key "$output_dir/server.key" \
    -out "$output_dir/server.csr" \
    -subj "/CN=nats-zone-z1" \
    -addext "basicConstraints=critical,CA:FALSE" \
    -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
    -addext "extendedKeyUsage=serverAuth" \
    -addext "subjectAltName=DNS:nats-zone-z1,DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1,IP:172.17.0.1"
  openssl x509 -req -sha256 -days 825 \
    -in "$output_dir/server.csr" \
    -CA "$ca_cert" -CAkey "$ca_key" -CAcreateserial \
    -copy_extensions copy -out "$output_dir/server.crt"
fi

if [[ ! -s "$output_dir/zone-control.key" || ! -s "$output_dir/zone-control.crt" ]]; then
  openssl genrsa -out "$output_dir/zone-control.key" 2048 >/dev/null 2>&1
  openssl req -new -sha256 -key "$output_dir/zone-control.key" \
    -out "$output_dir/zone-control.csr" \
    -subj "/CN=zone-control" \
    -addext "basicConstraints=critical,CA:FALSE" \
    -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
    -addext "extendedKeyUsage=clientAuth"
  openssl x509 -req -sha256 -days 825 \
    -in "$output_dir/zone-control.csr" \
    -CA "$ca_cert" -CAkey "$ca_key" -CAserial "$ca_serial" \
    -copy_extensions copy -out "$output_dir/zone-control.crt"
fi

if [[ ! -s "$output_dir/zone-public-authorizer.key" || ! -s "$output_dir/zone-public-authorizer.crt" ]]; then
  openssl genrsa -out "$output_dir/zone-public-authorizer.key" 2048 >/dev/null 2>&1
  openssl req -new -sha256 -key "$output_dir/zone-public-authorizer.key" \
    -out "$output_dir/zone-public-authorizer.csr" \
    -subj "/CN=zone-public-authorizer" \
    -addext "basicConstraints=critical,CA:FALSE" \
    -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
    -addext "extendedKeyUsage=clientAuth"
  openssl x509 -req -sha256 -days 825 \
    -in "$output_dir/zone-public-authorizer.csr" \
    -CA "$ca_cert" -CAkey "$ca_key" -CAserial "$ca_serial" \
    -copy_extensions copy -out "$output_dir/zone-public-authorizer.crt"
fi

if [[ ! -s "$output_dir/dataplane.key" || ! -s "$output_dir/dataplane.crt" ]]; then
  openssl genrsa -out "$output_dir/dataplane.key" 2048 >/dev/null 2>&1
  openssl req -new -sha256 -key "$output_dir/dataplane.key" \
    -out "$output_dir/dataplane.csr" \
    -subj "/CN=dataplane" \
    -addext "basicConstraints=critical,CA:FALSE" \
    -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
    -addext "extendedKeyUsage=clientAuth"
  openssl x509 -req -sha256 -days 825 \
    -in "$output_dir/dataplane.csr" \
    -CA "$ca_cert" -CAkey "$ca_key" -CAserial "$ca_serial" \
    -copy_extensions copy -out "$output_dir/dataplane.crt"
fi

rm -f \
  "$output_dir/server.csr" \
  "$output_dir/zone-control.csr" \
  "$output_dir/zone-public-authorizer.csr" \
  "$output_dir/dataplane.csr"

chmod 644 "$ca_cert" "$output_dir"/*.crt
chmod 600 "$ca_key" "$output_dir"/*.key

# Compose mounts these two ignored staging directories as one read-only tree so
# Docker does not need nested mounts below an already read-only secret mount.
mkdir -p "$zone_mount_dir" "$dataplane_mount_dir"
chmod 755 "$zone_mount_dir" "$dataplane_mount_dir"
for file in ca.crt server.crt server.key zone-control.crt zone-control.key \
  zone-public-authorizer.crt zone-public-authorizer.key dataplane.crt dataplane.key; do
  cp "$output_dir/$file" "$zone_mount_dir/$file"
  cp "$output_dir/$file" "$dataplane_mount_dir/$file"
done
chmod 644 "$zone_mount_dir"/*.crt "$dataplane_mount_dir"/*.crt
# Docker dev containers use distinct non-root UIDs, so the mounted leaf keys
# must be readable inside the isolated local stack. The canonical CA key and
# leaf keys under output_dir remain mode 0600.
chmod 644 "$zone_mount_dir"/*.key "$dataplane_mount_dir"/*.key
printf 'Generated shared dev CA and NATS mTLS leaves under %s\n' "$output_dir"
