# netbox discord webhook handler
dumb shim that pushes out netbox webhook events to a discord channel.

# Variables
* NETBOX_DOMAIN - Netbox base domain without https:// or anything. HTTPS only.
* DISCORD_WEBHOOK - Set this to your discord webhook.
* HOOK_SECRET - Webhook secret, set in the Netbox web panel.

#### Wrangler

To generate using [wrangler](https://github.com/cloudflare/wrangler)

```
wrangler generate projectname https://github.com/cloudflare/worker-template
```

#### Serverless

To deploy using serverless add a [`serverless.yml`](https://serverless.com/framework/docs/providers/cloudflare/) file.
