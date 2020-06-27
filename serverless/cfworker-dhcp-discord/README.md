# dhcp discord webhook handler
handles webhooks from the local dhcp servers and posts them to discord

## Requirement!
**This worker needs a Cloudflare Workers Unlimited plan because (ab)uses the Workers KV store ~~for something that honestly can be locally hosted~~.

# Variables
* DISCORD_WEBHOOK - Set this to your discord webhook.
* HOOK_SECRET - Webhook secret, keep this secret!
    - The hook secret will be required as a part of the request URL to "authenicate" requests.
* NETHOOKS - Name a CF Workers namespace this! Used for data storage.

#### Wrangler

To generate using [wrangler](https://github.com/cloudflare/wrangler)

```
wrangler generate projectname https://github.com/cloudflare/worker-template
```

#### Serverless

To deploy using serverless add a [`serverless.yml`](https://serverless.com/framework/docs/providers/cloudflare/) file.
