# atproto-pds kustomization

## Config
All ConfigMap values come from the upstream. Patch `pds-config` in your Kustomization as needed.

Common:
- PDS_HOSTNAME: "your-pds.example.com"

Other:
- PDS_DATA_DIRECTORY: "/data"
- PDS_BLOBSTORE_DISK_LOCATION: "/data/blocks"
- PDS_DID_PLC_URL: "https://plc.directory"
- PDS_BSKY_APP_VIEW_URL: "https://api.bsky.app"
- PDS_BSKY_APP_VIEW_DID: "did:web:api.bsky.app"
- PDS_REPORT_SERVICE_URL: "https://mod.bsky.app"
- PDS_REPORT_SERVICE_DID: "did:plc:ar7c4by46qjdydhdevvrndac"
- PDS_CRAWLERS: "https://bsky.network"
- LOG_ENABLED: "true"

## Secrets
Patch in secret `pds-secrets` with these values.

- PDS_JWT_SECRET: [can be generated with `openssl rand --hex 16`]
- PDS_ADMIN_PASSWORD: [can be generated with `openssl rand --hex 16`]
- PDS_PLC_ROTATION_KEY_K256_PRIVATE_KEY_HEX: (can be generated with `openssl ecparam --name secp256k1 --genkey --noout --outform DER | tail --bytes=+8 | head --bytes=32 | xxd --plain --cols 32`)
