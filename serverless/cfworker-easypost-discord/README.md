# easypost discord webhook handler
handles webhooks from easypost that have tracking info

# Variables
* DISCORD_WEBHOOK - Set this to your discord webhook.
* HOOK_SECRET - Webhook secret, keep this secret!
    - The hook secret will be required as a part of the request URL to "authenicate" requests.

#### Wrangler

To generate using [wrangler](https://github.com/cloudflare/wrangler)

```
wrangler generate projectname https://github.com/cloudflare/worker-template
```

#### Serverless

To deploy using serverless add a [`serverless.yml`](https://serverless.com/framework/docs/providers/cloudflare/) file.
